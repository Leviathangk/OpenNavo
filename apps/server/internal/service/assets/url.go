package assets

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/opennavo/opennavo/server/internal/fetch"
)

// DownloadURL reuses public-address resolution and IP-pinned connections, rejecting HTTPS downgrades on every redirect.
func (s *Service) DownloadURL(ctx context.Context, target string) ([]byte, error) {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "https" || fetch.ValidateURL(u) != nil {
		return nil, domain.Rejected("download_url_invalid")
	}
	client := fetch.NewClient(20 * time.Second)
	if s.URLClient != nil {
		copy := *s.URLClient
		client = &copy
	}
	redirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || fetch.ValidateURL(req.URL) != nil || len(via) > 5 {
			return domain.Rejected("download_redirect_invalid")
		}
		if redirect != nil {
			return redirect(req, via)
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, domain.Rejected("download_url_invalid")
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, &domain.AppError{Code: domain.CodeInvalidUpload, HTTPStatus: 400}
	}
	defer func() { _ = response.Body.Close() }()
	const maxBytes = 5 * 1024 * 1024
	if response.StatusCode != http.StatusOK || response.ContentLength > maxBytes {
		return nil, &domain.AppError{Code: domain.CodeInvalidUpload, HTTPStatus: 400}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil || len(data) > maxBytes {
		return nil, &domain.AppError{Code: domain.CodeInvalidUpload, HTTPStatus: 400}
	}
	// Upload/Preview image decoders revalidate MIME types and pixel dimensions.
	return data, nil
}
