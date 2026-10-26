package assets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path"
	"sync"
	"time"

	"github.com/opennavo/opennavo/server/internal/images"
	"github.com/opennavo/opennavo/server/internal/store"
	"golang.org/x/time/rate"
)

type BackfillOptions struct {
	Apply       bool
	Kind        string
	Limit       int
	Rate        float64
	Concurrency int
	Progress    func(BackfillProgress)
}

func (o BackfillOptions) Validate() error {
	if o.Kind != "" && o.Kind != "icon" && o.Kind != "screenshot" {
		return errors.New("kind must be icon or screenshot (omit for both)")
	}
	if o.Limit < 0 || o.Concurrency < 1 || o.Concurrency > 8 || math.IsNaN(o.Rate) || math.IsInf(o.Rate, 0) || o.Rate < 0.001 || o.Rate > 1000 {
		return errors.New("limit must be >=0, concurrency 1..8 and rate 0.001..1000 assets/second")
	}
	return nil
}

type BackfillProgress struct {
	Total, Processed, Written, Skipped, Failed, Files int64
	Deferred                                          int64
	// Remaining counts attempts left in this bounded run; Pending counts missing
	// manifests, including failed/unattempted rows and all previews in dry-run.
	Remaining, Pending int64
	ETA                time.Duration
	AssetID            int64
	Failure            string
	SkipReason         string
}

type backfillResult struct {
	id     int64
	append store.AssetRenditionAppendResult
	err    error
}

// BackfillRenditions uses a cursor, never OFFSET, so writes cannot move a later
// candidate out of this run. Failed IDs are retried on the next invocation.
func (s *Service) BackfillRenditions(ctx context.Context, options BackfillOptions) (BackfillProgress, error) {
	var progress BackfillProgress
	if err := options.Validate(); err != nil {
		return progress, err
	}
	total, throughID, err := s.Store.AssetRenditionSnapshot(ctx, options.Kind)
	if err != nil {
		return progress, fmt.Errorf("select asset backfill snapshot: %w", err)
	}
	budget := total
	if options.Limit > 0 {
		budget = min(budget, int64(options.Limit))
	}
	progress.Total, progress.Pending, progress.Remaining = total, total, budget
	started := time.Now()
	report := func() {
		throughput := options.Rate
		if progress.Processed > 0 {
			throughput = min(throughput, float64(progress.Processed)/time.Since(started).Seconds())
		}
		progress.ETA = time.Duration(float64(progress.Remaining) / throughput * float64(time.Second))
		if options.Progress != nil {
			options.Progress(progress)
		}
	}
	report()
	if budget == 0 {
		return progress, nil
	}
	jobs := make(chan int64)
	results := make(chan backfillResult, options.Concurrency)
	producerError := make(chan error, 1)
	go func() {
		defer close(jobs)
		var afterID, scheduled int64
		for scheduled < budget {
			ids, err := s.Store.AssetRenditionPage(ctx, options.Kind, afterID, throughID, int(min(100, budget-scheduled)))
			if err != nil {
				producerError <- err
				return
			}
			if len(ids) == 0 {
				return
			}
			for _, id := range ids {
				select {
				case jobs <- id:
					scheduled++
					afterID = id
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	limiter := rate.NewLimiter(rate.Limit(options.Rate), 1)
	var workers sync.WaitGroup
	for range options.Concurrency {
		workers.Go(func() {
			for id := range jobs {
				if err := limiter.Wait(ctx); err != nil {
					continue
				}
				result := backfillResult{id: id}
				if options.Apply {
					result.append, result.err = s.backfillAsset(ctx, id)
				}
				results <- result
			}
		})
	}
	go func() {
		workers.Wait()
		close(results)
	}()
	for result := range results {
		progress.Processed++
		progress.AssetID, progress.Failure, progress.SkipReason = result.id, "", ""
		progress.Remaining = max(0, budget-progress.Processed)
		switch {
		case result.err != nil:
			progress.Failed++
			progress.Failure = BackfillFailureReason(result.err)
		case result.append.Added > 0:
			progress.Written++
			progress.Files += int64(result.append.Added)
		case options.Apply:
			progress.Skipped++
		}
		if result.append.DeferredReason != "" {
			progress.Deferred++
			progress.SkipReason = result.append.DeferredReason
		}
		progress.Pending = max(0, total-progress.Written-progress.Skipped+progress.Deferred)
		report()
	}
	if ctx.Err() != nil {
		return progress, ctx.Err()
	}
	select {
	case err := <-producerError:
		return progress, fmt.Errorf("select asset backfill page: %w", err)
	default:
	}
	// Refresh the pending count once at completion, including concurrent runs.
	progress.Pending, _, err = s.Store.AssetRenditionSnapshot(ctx, options.Kind)
	progress.Remaining, progress.AssetID, progress.Failure, progress.SkipReason = 0, 0, "", ""
	report()
	return progress, err
}

type backfillObjects interface {
	Objects
	ReadLimited(context.Context, string, int64) ([]byte, error)
	// PutMissing must not overwrite an object; retries reuse an earlier upload.
	PutMissing(context.Context, string, []byte, string) error
}

type backfillError struct {
	stage string
	err   error
}

func (e *backfillError) Error() string { return e.stage + ": " + e.err.Error() }
func (e *backfillError) Unwrap() error { return e.err }

// BackfillFailureReason deliberately excludes raw driver/network errors, which
// can contain connection strings or signed object URLs.
func BackfillFailureReason(err error) string {
	stage := "manifest_transaction"
	var failure *backfillError
	if errors.As(err, &failure) {
		stage = failure.stage
	}
	if errors.Is(err, context.Canceled) {
		return stage + ":canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return stage + ":timeout"
	}
	return stage + ":" + backfillErrorCode(err)
}

func (s *Service) backfillAsset(ctx context.Context, id int64) (store.AssetRenditionAppendResult, error) {
	var result store.AssetRenditionAppendResult
	objects, ok := s.Objects.(backfillObjects)
	if !ok {
		return result, &backfillError{stage: "object_storage_capability", err: errors.New("read and conditional upload required")}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	asset, err := s.Store.AssetByID(ctx, id)
	if err != nil {
		return result, err
	}
	var existing []store.AssetFile
	if err := json.Unmarshal(asset.Files, &existing); err != nil {
		return result, errors.New("invalid asset manifest")
	}
	missing := store.MissingAssetRenditions(asset.Kind, existing)
	if len(missing) == 0 {
		return result, nil
	}
	files, err := prepareRenditions(ctx, objects, asset, missing)
	if err != nil {
		return result, err
	}
	return s.Store.AppendAssetRenditions(ctx, asset, files)
}

// All source IO, conversion and conditional object uploads finish before the
// append method opens a transaction or takes a row lock.
func prepareRenditions(ctx context.Context, objects backfillObjects, asset store.Asset, missing []store.AssetRenditionSpec) ([]store.AssetFile, error) {
	source, err := store.AssetRenditionSource(asset)
	if err != nil {
		return nil, &backfillError{stage: "select_source", err: err}
	}
	maxBytes := int64(images.MaxUploadBytes)
	normalized := source.Role == "normalized"
	if normalized {
		maxBytes = images.MaxNormalizedBytes
	}
	data, err := objects.ReadLimited(ctx, source.StorageKey, maxBytes)
	if err != nil {
		return nil, &backfillError{stage: "read_source", err: err}
	}
	variants, _, err := processImageSource(ctx, data, asset.Kind, normalized)
	if err != nil {
		return nil, &backfillError{stage: "process_image", err: err}
	}
	files := make([]store.AssetFile, 0, len(missing))
	prefix := path.Dir(asset.StorageKey) + "/"
	for _, rendition := range variants[0].Files {
		file := renditionFile(objects, prefix, variants[0].SHA256, rendition)
		for _, spec := range missing {
			if !spec.Matches(file) {
				continue
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if err := objects.PutMissing(ctx, file.StorageKey, rendition.Data, rendition.MIME); err != nil {
				return nil, &backfillError{stage: "upload_rendition", err: err}
			}
			files = append(files, file)
		}
	}
	return files, nil
}

func backfillSource(asset store.Asset) (string, error) {
	source, err := store.AssetRenditionSource(asset)
	return source.StorageKey, err
}
