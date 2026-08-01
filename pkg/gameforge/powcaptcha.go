package gameforge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"sync"
	"time"
)

const (
	powChallengeBaseURL = "https://pow-captcha.gameforge.com"

	// ChallengeTypePow is Gameforge's proof-of-work captcha: there is nothing to
	// look at, the client just burns CPU until a hash matches. ChallengeTypeImageDrop
	// is the older "click the matching icon" challenge. Gameforge hands out both
	// interchangeably for the same account, so the type must be read per challenge
	// rather than assumed.
	ChallengeTypePow       = "gf-pow-captcha"
	ChallengeTypeImageDrop = "gf-image-drop-captcha"

	// Observed targets are 20 bits (~1M hashes). Cap the search so a harder than
	// expected challenge fails loudly instead of spinning forever.
	powMaxNonce = int64(1) << 34
)

// errNoCaptchaSolver is returned when an image-drop challenge shows up but no
// solver callback was configured. Proof-of-work challenges never need one.
var errNoCaptchaSolver = errors.New("no captcha solver configured")

type challengeTypeResponse struct {
	Script string `json:"script"`
	Type   string `json:"type"`
}

type powChallengeResponse struct {
	Pow struct {
		Algorithm  string `json:"algorithm"`
		Challenges []struct {
			Salt   string `json:"salt"`
			Target string `json:"target"`
		} `json:"challenges"`
	} `json:"pow"`
	// Instrumentation is a JSON-encoded array of probes, nested as a string.
	Instrumentation string `json:"instrumentation"`
}

type powSolution struct {
	Salt  string `json:"salt"`
	Nonce string `json:"nonce"`
}

type powSolverMetrics struct {
	Path        string  `json:"path"`
	TotalMs     int64   `json:"totalMs"`
	ChallengeMs []int64 `json:"challengeMs"`
}

type powSubmission struct {
	Pow             []powSolution `json:"pow"`
	Instrumentation []int32       `json:"instrumentation"`
	Metrics         struct {
		Solver powSolverMetrics `json:"solver"`
	} `json:"metrics"`
}

type powSubmissionResponse struct {
	Status string `json:"status"`
}

// GetChallengeType reports which kind of captcha Gameforge issued for this
// challenge ID. Loading this endpoint is also what activates the challenge —
// the browser's captcha loader hits it before anything else.
func GetChallengeType(ctx context.Context, client HttpClient, challengeID string) (string, error) {
	body, err := powDoJSON(ctx, client, http.MethodGet, getChallengeURL(challengeBaseURL, challengeID), nil)
	if err != nil {
		return "", err
	}
	var out challengeTypeResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("failed to decode challenge type: %w", err)
	}
	if out.Type == "" {
		return "", errors.New("challenge type missing from response")
	}
	return out.Type, nil
}

// SolvePowCaptcha completes a gf-pow-captcha challenge without any user
// interaction: it fetches the proof-of-work challenges plus the instrumentation
// probes, computes both, and submits the result. The challenge is bound to the
// caller's cookie jar, so this must run on the same HttpClient that hit the
// login endpoint.
func SolvePowCaptcha(ctx context.Context, client HttpClient, challengeID string) error {
	body, err := powDoJSON(ctx, client, http.MethodGet, powChallengeBaseURL+"/api/challenge/"+challengeID, nil)
	if err != nil {
		return fmt.Errorf("failed to fetch pow challenge: %w", err)
	}
	var challenge powChallengeResponse
	if err := json.Unmarshal(body, &challenge); err != nil {
		return fmt.Errorf("failed to decode pow challenge: %w", err)
	}
	if algo := challenge.Pow.Algorithm; algo != "" && algo != "sha-256" {
		return errors.New("unsupported pow algorithm: " + algo)
	}
	if len(challenge.Pow.Challenges) == 0 {
		return errors.New("pow challenge carried no work items")
	}

	start := time.Now()
	solutions := make([]powSolution, len(challenge.Pow.Challenges))
	durations := make([]int64, len(challenge.Pow.Challenges))
	errs := make([]error, len(challenge.Pow.Challenges))

	// One goroutine per work item, mirroring the browser's one-worker-per-challenge
	// split, bounded so a long challenge list cannot swamp the host.
	sem := make(chan struct{}, max(1, runtime.NumCPU()))
	var wg sync.WaitGroup
	for i, work := range challenge.Pow.Challenges {
		wg.Add(1)
		go func(i int, salt, target string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			itemStart := time.Now()
			nonce, err := solvePow(ctx, salt, target)
			durations[i] = time.Since(itemStart).Milliseconds()
			if err != nil {
				errs[i] = err
				return
			}
			solutions[i] = powSolution{Salt: salt, Nonce: nonce}
		}(i, work.Salt, work.Target)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}

	results, err := runInstrumentation(challenge.Instrumentation)
	if err != nil {
		return fmt.Errorf("failed to run instrumentation probes: %w", err)
	}

	submission := powSubmission{Pow: solutions, Instrumentation: results}
	submission.Metrics.Solver = powSolverMetrics{
		Path:        "wasm",
		TotalMs:     time.Since(start).Milliseconds(),
		ChallengeMs: durations,
	}
	payload, err := json.Marshal(submission)
	if err != nil {
		return err
	}

	body, err = powDoJSON(ctx, client, http.MethodPost, powChallengeBaseURL+"/api/challenge/"+challengeID, payload)
	if err != nil {
		return fmt.Errorf("failed to submit pow solutions: %w", err)
	}
	var out powSubmissionResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return fmt.Errorf("failed to decode pow submission response: %w", err)
	}
	if out.Status != "solved" {
		return errors.New("pow captcha rejected, status: " + out.Status)
	}
	return nil
}

// solvePow finds a nonce such that SHA-256(salt || decimal(nonce)) starts with
// the bits of target. target is a hex prefix, so it constrains 4 bits per
// character — a 5-character target means the digest must start with 5 zero
// nibbles. Ported from Gameforge's pow-solver-js fallback.
func solvePow(ctx context.Context, salt, target string) (string, error) {
	bits := 4 * len(target)
	padded := target
	if len(padded)%2 == 1 {
		padded += "0"
	}
	targetBytes, err := hex.DecodeString(padded)
	if err != nil {
		return "", fmt.Errorf("invalid pow target %q: %w", target, err)
	}
	fullBytes := bits >> 3
	remBits := bits & 7
	var mask byte
	if remBits > 0 {
		mask = byte(0xFF << (8 - remBits))
	}

	prefix := []byte(salt)
	buf := make([]byte, 0, len(prefix)+20)
	for nonce := int64(0); nonce < powMaxNonce; nonce++ {
		if nonce%(1<<20) == 0 && ctx.Err() != nil {
			return "", ctx.Err()
		}
		buf = append(buf[:0], prefix...)
		buf = strconv.AppendInt(buf, nonce, 10)
		sum := sha256.Sum256(buf)
		if !bytes.Equal(sum[:fullBytes], targetBytes[:fullBytes]) {
			continue
		}
		if remBits > 0 && fullBytes < len(targetBytes) &&
			sum[fullBytes]&mask != targetBytes[fullBytes]&mask {
			continue
		}
		return strconv.FormatInt(nonce, 10), nil
	}
	return "", errors.New("pow solution not found for target " + target)
}

// powDoJSON performs a JSON request against the challenge services. Unlike the
// image-drop helpers it checks the status code, so a rejected or expired
// challenge surfaces as an error instead of an empty body.
func powDoJSON(ctx context.Context, client HttpClient, method, url string, payload []byte) ([]byte, error) {
	var bodyReader io.Reader
	if payload != nil {
		bodyReader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", applicationJson)
	if payload != nil {
		req.Header.Set(contentTypeHeaderKey, applicationJson)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s: HTTP %d: %s", method, url, resp.StatusCode, truncate(string(body), 200))
	}
	return body, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
