package localauth

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
)

func TestHashUsesPolicyParameters(t *testing.T) {
	encoded, err := Hash("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.memory != MemoryKiB || parsed.time != Time || parsed.parallelism != Parallelism ||
		len(parsed.salt) != SaltLength || len(parsed.key) != KeyLength {
		t.Fatalf("unexpected policy: %+v", parsed)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Fatalf("unexpected encoding: %s", encoded)
	}
}

func TestVerifyAcceptsOnlyTheOriginalPassword(t *testing.T) {
	encoded, err := Hash("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify("correct horse battery", encoded); err != nil {
		t.Fatal(err)
	}
	if err := Verify("correct horse batterY", encoded); !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("wrong password returned %v", err)
	}
}

func TestParseRejectsTamperedOrOversizedParameters(t *testing.T) {
	encoded, err := Hash("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"memory above bound":   strings.Replace(encoded, "m=65536", "m=1048576", 1),
		"time above bound":     strings.Replace(encoded, "t=3", "t=100", 1),
		"parallel above bound": strings.Replace(encoded, "p=4", "p=64", 1),
		"wrong algorithm":      strings.Replace(encoded, "argon2id", "argon2i", 1),
		"missing fields":       "$argon2id$v=19$m=65536,t=3,p=4$abc",
		"not base64":           strings.Replace(encoded, "$"+strings.Split(encoded, "$")[4], "$***", 1),
	}
	for name, value := range cases {
		if err := Verify("correct horse battery", value); !errors.Is(err, ErrInvalidHash) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestParseChecksBoundsBeforeAllocating(t *testing.T) {
	original := idKey
	calls := 0
	idKey = func(password, salt []byte, time, memory uint32, threads uint8, keyLen uint32) []byte {
		calls++
		return original(password, salt, time, memory, threads, keyLen)
	}
	defer func() { idKey = original }()

	encoded, err := Hash("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	calls = 0
	oversized := strings.Replace(encoded, "m=65536", "m=268435456", 1)
	if err := Verify("x", oversized); !errors.Is(err, ErrInvalidHash) {
		t.Fatalf("oversized memory: %v", err)
	}
	if calls != 0 {
		t.Fatalf("derivation ran before the bounds check: %d calls", calls)
	}
}

func TestUnknownUserDoesTheSameWorkAsAWrongPassword(t *testing.T) {
	original := idKey
	real := 0
	idKey = func(password, salt []byte, time, memory uint32, threads uint8, keyLen uint32) []byte {
		real++
		return original(password, salt, time, memory, threads, keyLen)
	}
	defer func() { idKey = original }()

	encoded, err := Hash("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	_ = DummyHash() // warm the lazily built dummy before counting
	before := real
	_ = Verify("wrong password", encoded)
	wrongPasswordWork := real - before
	before = real
	_ = DummyVerify("wrong password")
	dummyWork := real - before
	if wrongPasswordWork != 1 || dummyWork != 1 {
		t.Fatalf("derivations: wrong=%d dummy=%d", wrongPasswordWork, dummyWork)
	}
	dummyParams, err := parse(DummyHash())
	if err != nil {
		t.Fatal(err)
	}
	realParams, err := parse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if dummyParams.memory != realParams.memory || dummyParams.time != realParams.time ||
		dummyParams.parallelism != realParams.parallelism {
		t.Fatalf("dummy cost %+v differs from policy %+v", dummyParams, realParams)
	}
	if argon2.Version != 19 {
		t.Fatal("unexpected argon2 version")
	}
}
