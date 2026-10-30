package management

import (
	"context"
	"testing"

	"github.com/opennavo/opennavo/server/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestPackageVisibilityRequiresReason(t *testing.T) {
	for _, field := range []string{"hidden", "private"} {
		for _, reason := range []any{nil, "", " \n\t "} {
			t.Run(field+"/"+stringValue(reason), func(t *testing.T) {
				svc := &Service{}
				body := map[string]any{field: true}
				if reason != nil {
					body["visibilityReason"] = reason
				}
				_, err := svc.packageWrite(context.Background(), "UpdatePackageMeta", Input{Body: body})
				var app *domain.AppError
				require.ErrorAs(t, err, &app)
				require.Equal(t, domain.CodeValidation, app.Code)
				require.Equal(t, "rejected: visibility_reason_required:visibilityReason", app.Msg)
			})
		}
	}
}

func stringValue(value any) string {
	if value == nil {
		return "missing"
	}
	return value.(string)
}
