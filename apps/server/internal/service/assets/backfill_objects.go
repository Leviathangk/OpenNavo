package assets

import (
	"bytes"
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/minio/minio-go/v7"
)

var errRenditionObjectConflict = errors.New("existing rendition size mismatch")

// PutMissing skips previously uploaded content, including objects left by an
// interrupted transaction. The conditional write also closes the HEAD/PUT race.
func (s S3Objects) PutMissing(ctx context.Context, key string, data []byte, mime string) error {
	info, err := s.Client.S3.StatObject(ctx, s.Client.Bucket, key, minio.StatObjectOptions{})
	if err == nil {
		if info.Size != int64(len(data)) {
			return errRenditionObjectConflict
		}
		return nil
	}
	if code := minio.ToErrorResponse(err).Code; code != "NoSuchKey" && code != "NotFound" {
		return err
	}
	options := minio.PutObjectOptions{ContentType: mime, CacheControl: "public, max-age=31536000, immutable", DisableMultipart: true}
	options.SetMatchETagExcept("*")
	_, err = s.Client.S3.PutObject(ctx, s.Client.Bucket, key, bytes.NewReader(data), int64(len(data)), options)
	if minio.ToErrorResponse(err).StatusCode == http.StatusPreconditionFailed {
		info, statErr := s.Client.S3.StatObject(ctx, s.Client.Bucket, key, minio.StatObjectOptions{})
		if statErr != nil {
			return statErr
		}
		if info.Size != int64(len(data)) {
			return errRenditionObjectConflict
		}
		return nil
	}
	return err
}

func backfillErrorCode(err error) string {
	if errors.Is(err, errRenditionObjectConflict) {
		return "object_size_mismatch"
	}
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) {
		switch pgError.Code {
		case "55P03":
			return "row_lock_timeout"
		case "57014":
			return "query_canceled"
		default:
			return "database_error"
		}
	}
	var storageError minio.ErrorResponse
	if errors.As(err, &storageError) {
		switch storageError.Code {
		case "NoSuchKey", "NotFound":
			return "object_not_found"
		case "AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch":
			return "storage_access_denied"
		default:
			return "object_storage_error"
		}
	}
	// Image validation errors are fixed strings without request or object data.
	switch err.Error() {
	case "image too large", "unsupported image", "image dimensions exceed limits", "invalid icon dimensions", "corrupt image", "normalized image dimensions exceed limits", "no source object", "invalid asset manifest":
		return err.Error()
	}
	var staged *backfillError
	if errors.As(err, &staged) {
		return backfillErrorCode(staged.err)
	}
	return "operation_failed"
}
