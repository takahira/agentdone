package slack

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// AGENTDONE_STDOUT=1 prints the message and short-circuits before any POST (the
// URL here would fail/hang if it were actually contacted).
func TestPostStdout(t *testing.T) {
	t.Setenv("AGENTDONE_STDOUT", "1")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	postErr := Post("http://127.0.0.1:0/never-contacted", "hello-stdout")
	_ = w.Close()
	os.Stdout = orig
	out, _ := io.ReadAll(r)
	if postErr != nil {
		t.Fatalf("Post in stdout mode = %v, want nil", postErr)
	}
	if !strings.Contains(string(out), "hello-stdout") {
		t.Errorf("stdout = %q, want it to contain the message", string(out))
	}
}

// A transport failure must return an error with the webhook secret redacted —
// the URL IS the secret, and callers (e.g. `init`'s "Webhook test failed: %v")
// print the returned error to stdout / CI logs.
func TestPostRedactsSecretInReturnedError(t *testing.T) {
	t.Setenv("AGENTDONE_STDOUT", "")
	const secret = "SuperSecretTokenXYZ"
	// Port 1 is unbound: client.Post fails with a *url.Error embedding the URL.
	err := Post("https://127.0.0.1:1/services/T0/B1/"+secret, "hi")
	if err == nil {
		t.Fatal("Post to an unreachable webhook returned nil, want an error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("returned error leaks the webhook secret: %v", err)
	}
}

// transcript-derived text must not reach Slack with live control
// characters — "<!channel>" would mention everyone, "<url|label>" would render
// a disguised link, and "Vec<T>" would silently disappear from the message.
func TestPostEscapesSlackControlCharacters(t *testing.T) {
	var got struct {
		Text string `json:"text"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &got); err != nil {
			t.Errorf("payload is not JSON: %v", err)
		}
	}))
	defer srv.Close()
	if err := Post(srv.URL, `done <!channel> & fixed Vec<T>`); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if want := `done &lt;!channel&gt; &amp; fixed Vec&lt;T&gt;`; got.Text != want {
		t.Errorf("text = %q, want %q", got.Text, want)
	}
}

// a transport error's message embeds the full webhook URL — which IS
// the secret. The debug log line must carry only scheme://host.
func TestRedactStripsWebhookPath(t *testing.T) {
	secret := "https://hooks.slack.com/services/T000/B000/SECRETTOKEN"
	in := &url.Error{Op: "Post", URL: secret, Err: errors.New("dial tcp: connection refused")}
	got := redact(in).Error()
	if strings.Contains(got, "SECRETTOKEN") || strings.Contains(got, "/services/") {
		t.Errorf("redact leaked the webhook path: %s", got)
	}
	for _, want := range []string{"hooks.slack.com", "connection refused"} {
		if !strings.Contains(got, want) {
			t.Errorf("redact dropped %q from: %s", want, got)
		}
	}
	plain := errors.New("plain failure")
	if redact(plain) != plain {
		t.Error("redact must pass a non-url.Error through unchanged")
	}
}

func TestPostStatusHandling(t *testing.T) {
	t.Run("2xx is success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()
		if err := Post(srv.URL, "hi"); err != nil {
			t.Fatalf("Post on 200 = %v, want nil", err)
		}
	})

	t.Run("4xx/5xx is surfaced as an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "no_service", http.StatusNotFound)
		}))
		defer srv.Close()
		if err := Post(srv.URL, "hi"); err == nil {
			t.Fatal("Post on 404 = nil, want an error (invalid webhook must not look successful)")
		}
	})

	t.Run("empty URL is a no-op", func(t *testing.T) {
		if err := Post("", "hi"); err != nil {
			t.Fatalf("Post with empty URL = %v, want nil", err)
		}
	})
}

func TestPostRetriesRetryableFailures(t *testing.T) {
	t.Setenv("AGENTDONE_STDOUT", "")

	t.Run("429 then 200", func(t *testing.T) {
		waits := replaceRetrySleep(t)
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls++
			if calls == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		if err := Post(srv.URL, "hi"); err != nil {
			t.Fatalf("Post after 429 = %v, want nil", err)
		}
		if calls != 2 {
			t.Errorf("requests = %d, want 2", calls)
		}
		if got, want := *waits, []time.Duration{time.Second}; !slicesEqual(got, want) {
			t.Errorf("retry waits = %v, want %v", got, want)
		}
	})

	t.Run("503 with Retry-After is honored", func(t *testing.T) {
		waits := replaceRetrySleep(t)
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls++
			if calls == 1 {
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		if err := Post(srv.URL, "hi"); err != nil {
			t.Fatalf("Post after 503 = %v, want nil", err)
		}
		if got, want := *waits, []time.Duration{2 * time.Second}; !slicesEqual(got, want) {
			t.Errorf("retry waits = %v, want %v (Retry-After on a 5xx must be honored)", got, want)
		}
	})

	t.Run("500 keeps failing", func(t *testing.T) {
		waits := replaceRetrySleep(t)
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls++
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		err := Post(srv.URL, "hi")
		if err == nil {
			t.Fatal("Post after repeated 500s = nil, want an error")
		}
		if calls != maxPostAttempts {
			t.Errorf("requests = %d, want %d", calls, maxPostAttempts)
		}
		if got, want := *waits, []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}; !slicesEqual(got, want) {
			t.Errorf("retry waits = %v, want %v", got, want)
		}
		if !strings.Contains(err.Error(), "after 3 attempt(s)") || !strings.Contains(err.Error(), "500") {
			t.Errorf("error = %q, want attempt count and status", err)
		}
	})

	t.Run("400 does not retry", func(t *testing.T) {
		waits := replaceRetrySleep(t)
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls++
			w.WriteHeader(http.StatusBadRequest)
		}))
		defer srv.Close()

		err := Post(srv.URL, "hi")
		if err == nil {
			t.Fatal("Post on 400 = nil, want an error")
		}
		if calls != 1 {
			t.Errorf("requests = %d, want 1", calls)
		}
		if len(*waits) != 0 {
			t.Errorf("retry waits = %v, want none", *waits)
		}
		if !strings.Contains(err.Error(), "after 1 attempt(s)") || !strings.Contains(err.Error(), "400") {
			t.Errorf("error = %q, want attempt count and status", err)
		}
	})
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, time.September, 18, 12, 0, 0, 0, time.UTC)
	if got, ok := parseRetryAfter("3", now); !ok || got != 3*time.Second {
		t.Errorf("seconds Retry-After = %s, %v; want 3s, true", got, ok)
	}
	date := now.Add(2 * time.Second).Format(http.TimeFormat)
	if got, ok := parseRetryAfter(date, now); !ok || got != 2*time.Second {
		t.Errorf("date Retry-After = %s, %v; want 2s, true", got, ok)
	}
	// A huge but valid seconds value must not overflow time.Duration into a
	// negative (which would bypass the delivery budget and retry at once).
	if got, ok := parseRetryAfter("9223372037", now); !ok || got != maxRetryAfter {
		t.Errorf("huge Retry-After = %s, %v; want %s, true", got, ok, maxRetryAfter)
	}
	// Beyond int64 entirely: ParseInt fails, but it is still a valid delta-seconds.
	if got, ok := parseRetryAfter("999999999999999999999", now); !ok || got != maxRetryAfter {
		t.Errorf("int64-overflowing Retry-After = %s, %v; want %s, true", got, ok, maxRetryAfter)
	}
	// Leading zeros do not make a value large: this is 1 second.
	if got, ok := parseRetryAfter("0000000000000000001", now); !ok || got != time.Second {
		t.Errorf("zero-padded Retry-After = %s, %v; want 1s, true", got, ok)
	}
}

func replaceRetrySleep(t *testing.T) *[]time.Duration {
	t.Helper()
	original := sleepForRetry
	var waits []time.Duration
	sleepForRetry = func(delay time.Duration) { waits = append(waits, delay) }
	t.Cleanup(func() { sleepForRetry = original })
	return &waits
}

func slicesEqual(a, b []time.Duration) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
