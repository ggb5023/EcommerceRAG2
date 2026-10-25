// Package localauth implements the password and session primitives for the
// built-in account profile. It owns no HTTP routing and no authorization
// decisions; the gateway only turns a resolved session into the existing
// auth.Session and keeps signing scopes the same way as before.
package localauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// Policy parameters are recorded in docs/decisions/0008; changing them requires
// a new decision record. Verification always trusts the stored PHC parameters so
// hashes written under an older policy keep working.
const (
	MemoryKiB      uint32 = 64 * 1024
	Time           uint32 = 3
	Parallelism    uint8  = 4
	SaltLength            = 16
	KeyLength             = 32
	maxMemoryKiB   uint32 = 256 * 1024
	maxTime        uint32 = 10
	MaxParallelism uint8  = 8
	maxHashLength         = 512
)

var (
	// ErrInvalidHash means the stored value is not a usable argon2id PHC string.
	ErrInvalidHash = errors.New("localauth: invalid password hash")
	// ErrPasswordMismatch is returned for every wrong password.
	ErrPasswordMismatch = errors.New("localauth: password mismatch")
)

// idKey is a seam for tests that count derivation work.
var idKey = argon2.IDKey

type phc struct {
	memory      uint32
	time        uint32
	parallelism uint8
	salt        []byte
	key         []byte
}

// Hash derives a PHC string with the current policy.
func Hash(password string) (string, error) {
	salt := make([]byte, SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := idKey([]byte(password), salt, Time, MemoryKiB, Parallelism, KeyLength)
	return encode(MemoryKiB, Time, Parallelism, salt, key), nil
}

// Verify re-derives the password with the parameters stored in the hash.
// Parameters are validated against hard bounds before any memory is allocated,
// so a tampered row cannot turn a login attempt into an allocation attack.
func Verify(password, encoded string) error {
	parsed, err := parse(encoded)
	if err != nil {
		return err
	}
	derived := idKey([]byte(password), parsed.salt, parsed.time, parsed.memory, parsed.parallelism, uint32(len(parsed.key)))
	if subtle.ConstantTimeCompare(derived, parsed.key) != 1 {
		return ErrPasswordMismatch
	}
	return nil
}

func parse(encoded string) (phc, error) {
	var out phc
	if len(encoded) == 0 || len(encoded) > maxHashLength {
		return out, ErrInvalidHash
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return out, ErrInvalidHash
	}
	if parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return out, ErrInvalidHash
	}
	var memory, time uint32
	var parallelism uint8
	for _, field := range strings.Split(parts[3], ",") {
		name, value, found := strings.Cut(field, "=")
		if !found {
			return out, ErrInvalidHash
		}
		number, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return out, ErrInvalidHash
		}
		switch name {
		case "m":
			memory = uint32(number)
		case "t":
			time = uint32(number)
		case "p":
			if number > 255 {
				return out, ErrInvalidHash
			}
			parallelism = uint8(number)
		default:
			return out, ErrInvalidHash
		}
	}
	// Bounds first: argon2.IDKey allocates memory proportional to m.
	if memory < 8*1024 || memory > maxMemoryKiB || time == 0 || time > maxTime ||
		parallelism == 0 || parallelism > MaxParallelism {
		return out, ErrInvalidHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return out, ErrInvalidHash
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) < 16 || len(key) > 64 {
		return out, ErrInvalidHash
	}
	return phc{memory: memory, time: time, parallelism: parallelism, salt: salt, key: key}, nil
}

func encode(memory, time uint32, parallelism uint8, salt, key []byte) string {
	return "$argon2id$v=" + strconv.Itoa(argon2.Version) +
		"$m=" + strconv.FormatUint(uint64(memory), 10) +
		",t=" + strconv.FormatUint(uint64(time), 10) +
		",p=" + strconv.FormatUint(uint64(parallelism), 10) +
		"$" + base64.RawStdEncoding.EncodeToString(salt) +
		"$" + base64.RawStdEncoding.EncodeToString(key)
}

var dummy = sync.OnceValue(func() string {
	encoded, err := Hash("localauth-dummy-password")
	if err != nil {
		// Hash only fails when the system RNG fails; a fixed fallback keeps the
		// unknown-user path doing the same work instead of panicking.
		return encode(MemoryKiB, Time, Parallelism, make([]byte, SaltLength), make([]byte, KeyLength))
	}
	return encoded
})

// DummyHash returns a hash with the current policy, used to equalise the work
// performed for unknown usernames.
func DummyHash() string { return dummy() }

// DummyVerify performs one derivation against the dummy hash and always fails.
func DummyVerify(password string) error {
	_ = Verify(password, dummy())
	return ErrPasswordMismatch
}
