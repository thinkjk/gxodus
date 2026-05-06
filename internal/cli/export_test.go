package cli

import (
	"reflect"
	"sort"
	"testing"
)

func TestSelectFailedAccountsForRetry(t *testing.T) {
	// classifyFailures returns (authFailed, otherFailed).
	type res struct{ auth, other []string }
	got := res{}
	got.auth, got.other = classifyFailures([]accountResult{
		{Email: "a@x", Err: errSessionExpiredSentinel},
		{Email: "b@x", Err: nil}, // success
		{Email: "c@x", Err: errSomeOtherFailure},
		{Email: "d@x", Err: errSessionExpiredSentinel},
	})
	sort.Strings(got.auth)
	sort.Strings(got.other)
	wantAuth := []string{"a@x", "d@x"}
	wantOther := []string{"c@x"}
	if !reflect.DeepEqual(got.auth, wantAuth) {
		t.Errorf("auth = %v, want %v", got.auth, wantAuth)
	}
	if !reflect.DeepEqual(got.other, wantOther) {
		t.Errorf("other = %v, want %v", got.other, wantOther)
	}
}
