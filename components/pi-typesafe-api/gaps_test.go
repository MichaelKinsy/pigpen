package pitypesafe

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/components/typesafe/libraries/typesafe"
)

// Cases the mutation check showed unproven by the ported twins.

func TestGroupReadableKeyFileIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	isolate(t)
	_, _ = StoreAPIKey(validKey)
	_ = os.Chmod(CredentialsPath(), 0o640)
	if s, _ := KeySituationFor(nil); s.Kind != KeyUnusable {
		t.Fatalf("a group-readable key file must be unusable: %+v", s)
	}
}

func TestAskDefaultDeadlineIsFifteenSeconds(t *testing.T) {
	var remaining time.Duration
	judge := judgeFunc(func(ctx context.Context, _ typesafe.SystemOneRequest) (*Evaluation, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Error("Ask must set a deadline")
		}
		remaining = time.Until(deadline)
		return &Evaluation{SystemOneResult: &typesafe.SystemOneResult{}}, nil
	})
	Ask(context.Background(), judge, sampleRequest(), AskOptions{})
	if remaining < 14*time.Second || remaining > 15*time.Second {
		t.Fatalf("deadline in %v", remaining)
	}
}

func TestAbortedFailureStopsTheBatchToo(t *testing.T) {
	calls := 0
	judge := batchJudge(func(ctx context.Context, _ typesafe.SystemOneRequest) (*Evaluation, error) {
		calls++
		return nil, newError(CodeAborted, "cancelled")
	})
	batch := EvaluateMany(context.Background(), judge, []typesafe.SystemOneRequest{sampleRequest(), sampleRequest(), sampleRequest()}, BatchOptions{Concurrency: 1})
	if calls != 1 || batch.Skipped != 2 {
		t.Fatalf("calls=%d skipped=%d", calls, batch.Skipped)
	}
}

type batchJudge func(context.Context, typesafe.SystemOneRequest) (*Evaluation, error)

func (f batchJudge) Evaluate(ctx context.Context, r typesafe.SystemOneRequest) (*Evaluation, error) {
	return f(ctx, r)
}

func TestOnlyHTTPRejectionsDegradeTheKey(t *testing.T) {
	isolate(t)
	t.Setenv("TYPESAFE_API_KEY", "env-key-0123456789abcdef")
	// A 401 status on a non-HTTP failure code is not a rejection.
	RecordAuthFailure(&IntegrationError{Code: CodeResponse, Message: "odd", Status: 401}, time.Now())
	if !mustAuth(t, nil).Usable {
		t.Fatal("only an HTTP 401 or 403 rejects the key")
	}
	RecordAuthFailure(&IntegrationError{Code: CodeHTTP, Message: "forbidden", Status: 403}, time.Now())
	if mustAuth(t, nil).Usable {
		t.Fatal("a 403 rejects the key")
	}
	RecordAuthFailure(&IntegrationError{Code: CodeHTTP, Message: "gone", Status: 404}, time.Now())
	if !mustAuth(t, nil).Usable {
		t.Fatal("a 404 does not")
	}
}
