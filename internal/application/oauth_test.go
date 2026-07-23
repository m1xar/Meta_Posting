package application

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMissingStrings(t *testing.T) {
	require.Equal(
		t,
		[]string{"business_management"},
		missingStrings(
			[]string{"ads_management", "business_management", "ads_read"},
			[]string{"ads_read", "ads_management"},
		),
	)
}
