package management

import (
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/opennavo/opennavo/server/gen/adminapi"
	"github.com/opennavo/opennavo/server/internal/service/snapshot"
	"github.com/opennavo/opennavo/server/internal/store"
	"github.com/stretchr/testify/require"
)

func TestDecodePreservesExplicitNullAndNormalizesParams(t *testing.T) {
	in, err := Decode(adminapi.UpdatePackageMetaRequestObject{Id: 42}, []byte(`{"notes":null,"tags":[]}`), store.AuditActor{}, false)
	require.NoError(t, err)
	require.Equal(t, int64(42), in.ID)
	value, present := in.Body["notes"]
	require.True(t, present)
	require.Nil(t, value)
	_, present = in.Body["developer"]
	require.False(t, present)
	in, err = Decode(struct{ Params map[string]any }{map[string]any{"Current": 2, "Size": 10, "Kind": "formula"}}, nil, store.AuditActor{}, false)
	require.NoError(t, err)
	current, size, err := page(in)
	require.NoError(t, err)
	require.Equal(t, 2, current)
	require.Equal(t, 10, size)
	require.Equal(t, "formula", text(in.Params, "kind"))
	_, err = Decode(nil, []byte(`invalid`), store.AuditActor{}, false)
	require.Error(t, err)
}
func TestIdsRejectDuplicatesFractionsAndOverflow(t *testing.T) {
	for _, values := range []any{nil, []any{float64(1), float64(1)}, []any{float64(1.5)}, []any{float64(0)}, []any{float64(math.MaxInt64)}, []any{"1"}} {
		_, err := ids(values)
		require.Error(t, err)
	}
	result, err := ids([]any{float64(1), float64(2)})
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2}, result)
}
func TestAuditRedactsNestedCredentials(t *testing.T) {
	in := map[string]any{"passwordHash": "bad", "settings": map[string]any{"apiKey": "bad", "title": "safe"}, "items": []any{map[string]any{"secret": "bad", "enabled": true}}}
	require.Equal(t, map[string]any{"settings": map[string]any{"title": "safe"}, "items": []any{map[string]any{"enabled": true}}}, redact(in))
}

func TestMetadataNullableFields(t *testing.T) {
	in, err := Decode(adminapi.UpdatePackageMetaRequestObject{Id: 1}, []byte(`{"downloadSize":null,"accentColor":null}`), store.AuditActor{}, false)
	require.NoError(t, err)
	require.Contains(t, in.Body, "downloadSize")
	require.Nil(t, in.Body["downloadSize"])
	require.Contains(t, in.Body, "accentColor")
	require.Nil(t, in.Body["accentColor"])
}

func TestDownloadSizeRetainsInt64Precision(t *testing.T) {
	for _, body := range []string{`{"downloadSize":9223372036854775807}`, `{"downloadSize":9007199254740993}`} {
		in, err := Decode(adminapi.UpdatePackageMetaRequestObject{Id: 1}, []byte(body), store.AuditActor{}, false)
		require.NoError(t, err)
		n, ok := in.Body["downloadSize"].(int64)
		require.True(t, ok)
		require.Equal(t, body, fmt.Sprintf(`{"downloadSize":%d}`, n))
	}
	for _, body := range []string{`{"downloadSize":9223372036854775808}`, `{"downloadSize":-1}`, `{"downloadSize":1.5}`} {
		_, err := Decode(adminapi.UpdatePackageMetaRequestObject{Id: 1}, []byte(body), store.AuditActor{}, false)
		require.Error(t, err)
	}
}

func TestRevocationResultTreatsSupersededAsWarning(t *testing.T) {
	warning, err := revocationResult("", snapshot.ErrRevocationSuperseded)
	require.NoError(t, err)
	require.Equal(t, snapshot.ConvergingWarning, warning)

	warning, err = revocationResult(snapshot.PurgePendingWarning, snapshot.ErrRevocationSuperseded)
	require.NoError(t, err)
	require.Equal(t, snapshot.PurgePendingWarning, warning)

	boom := errors.New("snapshot storage unavailable")
	warning, err = revocationResult("", boom)
	require.ErrorIs(t, err, boom)
	require.Empty(t, warning)
}
