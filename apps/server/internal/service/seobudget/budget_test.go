package seobudget

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestReceiptAmountsStayExact(t *testing.T) {
	for raw, expected := range map[string]int64{"0": 0, "0.000001": 1, "0.1": 100000, "15.00": 15000000, "99999999.999999": 99999999999999} {
		got, err := Micros(raw)
		require.NoError(t, err)
		require.Equal(t, expected, got)
	}
	for _, invalid := range []string{"-1", "1e-3", ".1", "01", "NaN", "0.0000001", "1000000000"} {
		_, err := Micros(invalid)
		require.Error(t, err)
	}
}
