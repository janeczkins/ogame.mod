package gameforge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testdata/pow_challenge.json is a real gf-pow-captcha payload captured from
// Gameforge, so these tests exercise the actual salts, targets and probe code.
func loadPowChallenge(t *testing.T) powChallengeResponse {
	t.Helper()
	raw, err := os.ReadFile("testdata/pow_challenge.json")
	require.NoError(t, err)
	var challenge powChallengeResponse
	require.NoError(t, json.Unmarshal(raw, &challenge))
	return challenge
}

func TestSolvePow(t *testing.T) {
	challenge := loadPowChallenge(t)
	require.Equal(t, "sha-256", challenge.Pow.Algorithm)
	require.Len(t, challenge.Pow.Challenges, 10)

	for _, work := range challenge.Pow.Challenges {
		nonce, err := solvePow(context.Background(), work.Salt, work.Target)
		require.NoError(t, err)
		sum := sha256.Sum256([]byte(work.Salt + nonce))
		assert.True(t, strings.HasPrefix(hex.EncodeToString(sum[:]), work.Target),
			"digest for salt %s must start with %s, got %s",
			work.Salt, work.Target, hex.EncodeToString(sum[:8]))
	}
}

func TestSolvePowHonoursCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// "ffffffffff" is unreachable in practice, so the solver can only stop by
	// noticing the cancellation.
	_, err := solvePow(ctx, "salt", "ffffffffff")
	assert.ErrorIs(t, err, context.Canceled)
}

func TestRunInstrumentation(t *testing.T) {
	challenge := loadPowChallenge(t)
	results, err := runInstrumentation(challenge.Instrumentation)
	require.NoError(t, err)
	require.Len(t, results, 15)

	// Expected values for every probe family that is deterministic. Canvas
	// probes (indexes 2, 3, 6) are machine-seeded and checked separately.
	for _, tc := range []struct {
		index int
		want  int32
		why   string
	}{
		{0, 210, "clientWidth = 200px width + 2x5px padding"},
		{1, 165, "clientHeight = 125px height + 2x20px padding"},
		{14, 100, "offsetHeight = 100px height, no padding or border"},
		{4, 57906, "5 environment guards, all true"},
		{5, 50580, "4 environment guards, all true"},
		{9, 4397, "4 environment guards, all true"},
		{10, 22584, "3 environment guards, all true"},
		{12, 28670, "4 environment guards, all true"},
		{7, 5, "bitwise chain"},
		{8, 130539, "bitwise chain"},
		{11, 262123, "bitwise chain"},
		{13, 17759, "bitwise chain"},
	} {
		assert.Equal(t, tc.want, results[tc.index], "probe %d: %s", tc.index, tc.why)
	}

	// Probes 2 and 6 ship identical code; a real browser returns the same value
	// for both, and disagreement is exactly what the duplicate is there to catch.
	assert.Equal(t, results[2], results[6], "identical canvas probes must agree")
	assert.NotEqual(t, results[2], results[3], "different canvas probes must differ")
	for _, i := range []int{2, 3, 6} {
		assert.NotZero(t, results[i], "canvas probe %d must not look like a failure", i)
	}
}

func TestEvalDomBoxModel(t *testing.T) {
	for _, tc := range []struct {
		name string
		code string
		want int32
	}{
		{
			name: "border adds to offset but not client",
			code: `el.style.width = '100px'; el.style.padding = '10px'; el.style.borderWidth = '2px'; return el.offsetWidth;`,
			want: 124,
		},
		{
			name: "client excludes border",
			code: `el.style.width = '100px'; el.style.padding = '10px'; el.style.borderWidth = '2px'; return el.clientWidth;`,
			want: 120,
		},
		{
			name: "border-box width already includes padding",
			code: `el.style.width = '100px'; el.style.padding = '10px'; el.style.boxSizing = 'border-box'; return el.clientWidth;`,
			want: 100,
		},
		{
			name: "empty element with no height collapses",
			code: `el.style.width = '100px'; return el.clientHeight;`,
			want: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, evalDom(tc.code))
		})
	}
}

func TestEvalGuard(t *testing.T) {
	for expr, want := range map[string]bool{
		"typeof window !== 'undefined'":    true,
		"typeof navigator !== 'undefined'": true,
		"typeof eval === 'function'":       true,
		"typeof document === 'object'":     true,
		// Automation/Node globals must not be present in a browser.
		"typeof process !== 'undefined'": false,
		"typeof require === 'function'":  false,
		"Function.prototype.toString.call(eval).indexOf('[native code]') !== -1": true,
		"Function.prototype.toString.call(eval).indexOf('[native code]') === -1": false,
	} {
		assert.Equal(t, want, evalGuard(expr), expr)
	}
}

func TestEvalBitwiseJavaScriptSemantics(t *testing.T) {
	// `<< 24` overflows into the sign bit, which `| 0` keeps as a negative int32.
	assert.Equal(t, int32(-16777216), evalBitwise(`var v = 255; v = (v << 24) | 0; return v;`))
	// `>>` is sign-propagating in JavaScript, unlike `>>>`.
	assert.Equal(t, int32(-1), evalBitwise(`var v = -1; v = (v >> 4) | 0; return v;`))
	assert.Equal(t, int32(268435455), evalBitwise(`var v = -1; v = (v >>> 4) | 0; return v;`))
}

func TestRunInstrumentationUnknownProbeYieldsZero(t *testing.T) {
	// A probe a browser cannot run returns 0; an unmodelled family must too.
	results, err := runInstrumentation(`[{"id":"x","type":"webgl","code":"return 1;"}]`)
	require.NoError(t, err)
	assert.Equal(t, []int32{0}, results)
}
