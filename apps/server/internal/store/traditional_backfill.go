package store

import (
	"context"
	"errors"
	"time"

	"github.com/opennavo/opennavo/server/internal/content"
	"github.com/opennavo/opennavo/server/internal/traditional"
	"gorm.io/gorm"
)

// TraditionalBackfill paces RunTraditionalBackfill. A new converter Version derives every stored
// object again: the limits keep that from crowding out catalog writes, and keep its writes (each a
// page refresh, cache invalidation and URL notification) to a trickle. Zero means no limit.
type TraditionalBackfill struct {
	DryRun bool
	// ObjectsPerSecond limits objects derived again: Simplified text ready and no receipt from this
	// converter, or Traditional text to withdraw. Objects already current are only checked.
	ObjectsPerSecond float64
	// WritesPerMinute limits derived rows written or withdrawn. Text that comes out the same only has
	// its receipt renewed, which costs no write.
	WritesPerMinute float64
	// Progress receives the counts at the start and after every 500 objects derived again, when the
	// run has any, and once at the end.
	Progress func(TraditionalProgress)
	Now      func() time.Time
	Sleep    func(context.Context, time.Duration) error
}

// TraditionalProgress counts a backfill run.
type TraditionalProgress struct {
	// Objects and Pending are counted at the start: every object, and those to derive again.
	Objects, Pending int
	// Processed counts objects checked; Stamped, Written and Failed what deriving them did.
	Processed, Stamped, Written, Failed int
	// Remaining are pending objects not reached yet; ETA estimates them at the limits.
	Remaining int
	ETA       time.Duration
	Elapsed   time.Duration
	Done      bool
}

// BackfillTraditional derives every object without limits; the offline CLI uses RunTraditionalBackfill.
func (s *Store) BackfillTraditional(ctx context.Context, dryRun bool) (map[string]int, error) {
	return s.RunTraditionalBackfill(ctx, TraditionalBackfill{DryRun: dryRun})
}

// RunTraditionalBackfill is restartable: receipts record the converter, so a later run passes over
// objects already done. It reads 128 candidates per page and derives one object per transaction,
// with the same locks and cache outbox as live edits; the catalog lock is never held while waiting.
// It counts writes per entity, "<entity>:stamped" for renewed receipts and "<entity>:failed" for
// text that cannot be derived.
func (s *Store) RunTraditionalBackfill(ctx context.Context, opts TraditionalBackfill) (map[string]int, error) {
	now, sleep := opts.Now, opts.Sleep
	if now == nil {
		now = time.Now
	}
	if sleep == nil {
		sleep = sleepContext
	}
	objects, writes := pacer{interval: rateInterval(time.Second, opts.ObjectsPerSecond)}, pacer{interval: rateInterval(time.Minute, opts.WritesPerMinute)}
	// A preview rolls its writes back, so only a real run waits for its write budget.
	hold := !opts.DryRun && writes.interval > 0
	started := now()
	progress := TraditionalProgress{}
	for _, entity := range traditionalEntities() {
		total, pending, err := s.countTraditional(ctx, entity)
		if err != nil {
			return nil, err
		}
		progress.Objects += total
		progress.Pending += pending
	}
	reached := 0
	report := func(done bool) {
		if opts.Progress == nil {
			return
		}
		progress.Remaining = max(progress.Pending-reached, 0)
		progress.ETA = time.Duration(progress.Remaining) * objects.interval
		if reached > 0 {
			expected := float64(progress.Remaining) * float64(progress.Written) / float64(reached)
			progress.ETA = max(progress.ETA, time.Duration(expected*float64(writes.interval)))
		}
		progress.Elapsed, progress.Done = now().Sub(started), done
		opts.Progress(progress)
	}
	if progress.Pending > 0 {
		report(false)
	}
	counts := map[string]int{}
	for _, entity := range traditionalEntities() {
		cursor := ""
		for {
			rows, err := s.traditionalCandidates(ctx, entity, cursor)
			if err != nil {
				return counts, err
			}
			for _, r := range rows {
				cursor = r.ObjectKey
				pending := r.pending()
				if pending {
					if err := objects.wait(ctx, now, sleep); err != nil {
						return counts, err
					}
				}
				outcome, err := s.backfillTraditionalObject(ctx, entity, r, opts.DryRun, hold)
				if err == nil && outcome == traditionalHeld {
					if err = writes.wait(ctx, now, sleep); err == nil {
						outcome, err = s.backfillTraditionalObject(ctx, entity, r, opts.DryRun, false)
					}
				}
				switch {
				case errors.Is(err, errTraditionalInvalid):
					counts[entity+":failed"]++
					progress.Failed++
				case err != nil:
					return counts, err
				case outcome == traditionalWritten:
					counts[entity]++
					progress.Written++
				case outcome == traditionalStamped:
					counts[entity+":stamped"]++
					progress.Stamped++
				}
				progress.Processed++
				if pending {
					reached++
					if reached%500 == 0 {
						report(false)
					}
				}
			}
			if len(rows) < 128 {
				break
			}
		}
	}
	report(true)
	return counts, nil
}

// traditionalCandidate is one object with Simplified or Traditional text.
type traditionalCandidate struct {
	ObjectKey       string
	ID, SecondaryID int64
	// Derivable: zh-CN text is ready to derive from; Derived: zh-Hant text exists; Current: the
	// receipt is from this converter.
	Derivable, Derived, Current bool
}

// pending objects are derived again; the others are only checked.
func (c traditionalCandidate) pending() bool {
	return (c.Derivable && !c.Current) || (c.Derived && !c.Derivable)
}

// traditionalColumns are an entity's object key, ID and secondary ID over its text table i, and the
// condition for a zh-CN row to derive from.
func traditionalColumns(entity string) (key, id, secondary, ready string) {
	spec := traditionalSpec(entity)
	key, id, secondary, ready = "i."+spec.Key+"::text", "i."+spec.Key, "0", "i.status IN ('source','manual','machine')"
	if entity == "collection_item" {
		key += "||':'||i.package_id::text"
		secondary = "i.package_id"
	}
	if entity == "announcement" || entity == "background_item" {
		id = "0"
	}
	if _, auxiliary := traditionalAuxiliary[entity]; auxiliary {
		ready = "true" // proof-backed texts have no translation status
	}
	return key, id, secondary, ready
}

func (s *Store) traditionalCandidates(ctx context.Context, entity, cursor string) ([]traditionalCandidate, error) {
	key, id, secondary, ready := traditionalColumns(entity)
	var rows []traditionalCandidate
	err := s.DB.WithContext(ctx).Raw(`SELECT c.*,d.object_key IS NOT NULL current FROM (SELECT `+key+` object_key,`+id+` id,`+secondary+` secondary_id,bool_or(i.locale='zh-CN' AND `+ready+`) derivable,bool_or(i.locale='zh-Hant') derived
 FROM `+traditionalSpec(entity).Table+` i WHERE i.locale IN ('zh-CN','zh-Hant') AND `+key+`>? GROUP BY 1,2,3 ORDER BY 1 LIMIT 128) c
 LEFT JOIN derived_traditional d ON d.entity=? AND d.object_key=c.object_key AND d.converter=? AND d.restored_at IS NULL ORDER BY c.object_key`, cursor, entity, traditional.Version).Scan(&rows).Error
	return rows, err
}

// countTraditional counts an entity's objects and those a run derives again.
func (s *Store) countTraditional(ctx context.Context, entity string) (int, int, error) {
	key, _, _, ready := traditionalColumns(entity)
	var n struct{ Objects, Pending int }
	err := s.DB.WithContext(ctx).Raw(`SELECT count(*) objects,count(*) FILTER (WHERE (c.derivable AND d.object_key IS NULL) OR (c.derived AND NOT c.derivable)) pending
 FROM (SELECT `+key+` object_key,bool_or(i.locale='zh-CN' AND `+ready+`) derivable,bool_or(i.locale='zh-Hant') derived FROM `+traditionalSpec(entity).Table+` i WHERE i.locale IN ('zh-CN','zh-Hant') GROUP BY 1) c
 LEFT JOIN derived_traditional d ON d.entity=? AND d.object_key=c.object_key AND d.converter=? AND d.restored_at IS NULL`, entity, traditional.Version).Scan(&n).Error
	return n.Objects, n.Pending, err
}

// backfillTraditionalObject derives one object in its own transaction; a preview rolls it back.
func (s *Store) backfillTraditionalObject(ctx context.Context, entity string, r traditionalCandidate, dryRun, hold bool) (traditionalOutcome, error) {
	outcome := traditionalUnchanged
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := LockCatalogWrites(ctx, tx); err != nil {
			return err
		}
		st := &Store{DB: tx, Redis: s.Redis}
		var err error
		if _, auxiliary := traditionalAuxiliary[entity]; auxiliary {
			outcome, err = st.deriveAuxiliary(ctx, entity, r.ObjectKey, hold, true)
		} else {
			outcome, err = st.deriveTraditional(ctx, content.Ref{Entity: entity, ID: r.ID, SecondaryID: r.SecondaryID}, hold)
		}
		if err != nil {
			return err
		}
		if dryRun {
			return errTraditionalPreview
		}
		return nil
	})
	if errors.Is(err, errTraditionalPreview) {
		err = nil
	}
	return outcome, err
}

// pacer spaces events at least interval apart; a zero interval never waits.
type pacer struct {
	interval time.Duration
	next     time.Time
}

func (p *pacer) wait(ctx context.Context, now func() time.Time, sleep func(context.Context, time.Duration) error) error {
	if p.interval <= 0 {
		return ctx.Err()
	}
	at := now()
	if at.Before(p.next) {
		if err := sleep(ctx, p.next.Sub(at)); err != nil {
			return err
		}
		at = p.next
	}
	p.next = at.Add(p.interval)
	return nil
}

// rateInterval spaces rate events per period; no positive rate means no limit.
func rateInterval(period time.Duration, rate float64) time.Duration {
	if rate <= 0 {
		return 0
	}
	return time.Duration(float64(period) / rate)
}

func sleepContext(ctx context.Context, wait time.Duration) error {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
