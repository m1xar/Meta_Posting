package application

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/watchers-factory/raze-ads/internal/domain"
)

func TestNormalizeEmailAcceptsRealAddresses(t *testing.T) {
	for _, input := range []string{
		"buyer@example.com", "  Buyer@Example.COM  ", "first.last+tag@sub.example.co.uk",
	} {
		email, err := normalizeEmail(input)
		require.NoError(t, err, input)
		require.Equal(t, email, lower(email), "must be stored lowercased")
		require.Contains(t, email, "@")
	}

	email, err := normalizeEmail("  Buyer@Example.COM  ")
	require.NoError(t, err)
	require.Equal(t, "buyer@example.com", email)
}

func TestNormalizeEmailRejectsWhatARegexWouldAccept(t *testing.T) {
	// A display-name form parses as a valid address but must never become an
	// identity: the stored string would not be the address.
	_, err := normalizeEmail("Buyer Name <buyer@example.com>")
	require.Error(t, err)

	for _, input := range []string{
		"", "   ", "no-at-sign", "@example.com", "buyer@", "buyer@@example.com",
		"buyer name@example.com", "buyer@exam ple.com",
	} {
		_, err := normalizeEmail(input)
		require.Error(t, err, "input %q must be rejected", input)
	}
}

func TestNormalizeEmailEnforcesLengthLimits(t *testing.T) {
	long := make([]byte, 250)
	for i := range long {
		long[i] = 'a'
	}
	_, err := normalizeEmail(string(long) + "@example.com")
	require.ErrorContains(t, err, "254")

	// A local part over 64 characters is invalid even when the whole address
	// is short enough.
	local := make([]byte, 65)
	for i := range local {
		local[i] = 'a'
	}
	_, err = normalizeEmail(string(local) + "@example.com")
	require.Error(t, err)
}

func TestUsernamePatternAndReservedNames(t *testing.T) {
	for _, valid := range []string{"buyer", "buyer.one", "buyer-two", "b2b_x", "abc"} {
		require.True(t, usernamePattern.MatchString(valid), valid)
	}
	for _, invalid := range []string{"ab", "-leading", ".leading", "Upper", "has space", "has@at"} {
		require.False(t, usernamePattern.MatchString(invalid), invalid)
	}

	// Names that would let a user impersonate the platform or shadow a route.
	for _, reserved := range []string{"admin", "root", "api", "support", "legacy-admin", "v1"} {
		_, found := reservedUsernames[reserved]
		require.True(t, found, reserved)
	}
}

func TestUserRoleCapabilities(t *testing.T) {
	require.True(t, domain.User{Role: domain.RoleAdmin}.IsAdmin())
	require.False(t, domain.User{Role: domain.RoleUser}.IsAdmin())

	require.True(t, domain.User{Role: domain.RoleUser}.CanLogin())
	require.True(t, domain.User{Role: domain.RoleAdmin}.CanLogin())
	// The legacy tenant holds pre-multi-tenant data and must never sign in.
	require.False(t, domain.User{Role: domain.RoleDisabled}.CanLogin())
}

func lower(value string) string {
	out := []byte(value)
	for i, c := range out {
		if c >= 'A' && c <= 'Z' {
			out[i] = c + 32
		}
	}
	return string(out)
}
