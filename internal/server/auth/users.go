package auth

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters for new hashes: the RFC 9106 "second recommended" set,
// 64 MiB, 3 passes, 4 lanes. A hash written with other parameters is verified
// with its own, so an operator's older file keeps working.
const (
	argonMemory  = 64 * 1024
	argonTime    = 3
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16

	// maxArgonMemory bounds what a users file can ask this process to allocate
	// per sign-in. A file is the operator's, but a typo of a few more digits
	// should not be a denial of service waiting for the next login.
	maxArgonMemory = 1 << 20 // 1 GiB, in KiB
	maxArgonTime   = 20
)

// Users is the accounts of the password provider, from a file the operator
// supplies. The server creates no accounts and stores no credential: the file is
// read at start-up and what is in it is a hash.
type Users struct {
	byName map[string]user
	// decoy is a hash of nothing, with a real account's cost, verified against
	// when an account does not exist, so a name that is not an account takes as long to refuse as one that
	// is and the response time does not say which names exist.
	decoy parsedHash

	// observe, when set, is called around each hash. Tests use it to count hashes
	// in flight; production leaves it nil.
	observe func() func()
}

type user struct {
	name string
	hash parsedHash
	// Groups the account is in, for the role rules.
	groups []string
}

type parsedHash struct {
	memory  uint32
	time    uint32
	threads uint8
	salt    []byte
	key     []byte
}

// HashPassword returns the PHC string for a password, for the helper that
// writes users files. It is not used at sign-in.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// ParseUsers reads a users file: one account per line,
//
//	name:$argon2id$v=19$m=65536,t=3,p=4$salt$hash[:group,group]
//
// Blank lines and lines starting with # are skipped. Only argon2id is accepted;
// a line holding a plain password, or any other scheme, is an error that names
// the line and not the text, so a file with a secret in it is refused and the
// secret is not copied into a log.
func ParseUsers(r io.Reader) (*Users, error) {
	u := &Users{byName: map[string]user{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, rest, ok := strings.Cut(line, ":")
		if !ok || name == "" || strings.ContainsAny(name, " \t") {
			return nil, fmt.Errorf("users file line %d: expected name:hash", n)
		}
		hash, groups, _ := strings.Cut(rest, ":")
		ph, err := parseHash(hash)
		if err != nil {
			return nil, fmt.Errorf("users file line %d: %w", n, err)
		}
		key := strings.ToLower(name)
		if _, dup := u.byName[key]; dup {
			return nil, fmt.Errorf("users file line %d: %q appears twice", n, name)
		}
		var gs []string
		for _, g := range strings.Split(groups, ",") {
			if g = strings.TrimSpace(g); g != "" {
				gs = append(gs, g)
			}
		}
		u.byName[key] = user{name: name, hash: ph, groups: gs}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(u.byName) == 0 {
		return nil, errors.New("the users file has no accounts")
	}
	// The decoy costs what a real account costs: it takes the parameters of one
	// of them, with a salt and key nobody knows. A decoy at the default cost
	// beside accounts at another would make unknown names the slower ones, and
	// the timing would say which names exist.
	for _, first := range u.byName {
		u.decoy = parsedHash{memory: first.hash.memory, time: first.hash.time, threads: first.hash.threads,
			salt: randomBytes(len(first.hash.salt)), key: randomBytes(len(first.hash.key))}
		break
	}
	return u, nil
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the system's random source failing is not a state to continue in
	}
	return b
}

func parseHash(s string) (parsedHash, error) {
	p := strings.Split(s, "$")
	// "", "argon2id", "v=19", "m=..,t=..,p=..", salt, key
	if len(p) != 6 || p[0] != "" || p[1] != "argon2id" {
		return parsedHash{}, errors.New("the hash is not an argon2id PHC string; no plain password or other scheme is accepted")
	}
	if p[2] != "v="+strconv.Itoa(argon2.Version) {
		return parsedHash{}, errors.New("the argon2 version is not 19")
	}
	var ph parsedHash
	for _, kv := range strings.Split(p[3], ",") {
		k, v, ok := strings.Cut(kv, "=")
		n, err := strconv.ParseUint(v, 10, 32)
		if !ok || err != nil {
			return parsedHash{}, errors.New("the argon2 parameters are malformed")
		}
		switch k {
		case "m":
			ph.memory = uint32(n)
		case "t":
			ph.time = uint32(n)
		case "p":
			if n > 255 {
				return parsedHash{}, errors.New("the argon2 parallelism is out of range")
			}
			ph.threads = uint8(n)
		default:
			return parsedHash{}, errors.New("the argon2 parameters are malformed")
		}
	}
	if ph.memory < 8*uint32(ph.threads) || ph.memory > maxArgonMemory || ph.time == 0 || ph.time > maxArgonTime || ph.threads == 0 {
		return parsedHash{}, errors.New("the argon2 parameters are outside what this server accepts")
	}
	var err error
	if ph.salt, err = base64.RawStdEncoding.DecodeString(p[4]); err != nil || len(ph.salt) < 8 {
		return parsedHash{}, errors.New("the argon2 salt is malformed or short")
	}
	if ph.key, err = base64.RawStdEncoding.DecodeString(p[5]); err != nil || len(ph.key) < 16 {
		return parsedHash{}, errors.New("the argon2 hash is malformed or short")
	}
	return ph, nil
}

func (h parsedHash) verify(password string) bool {
	got := argon2.IDKey([]byte(password), h.salt, h.time, h.memory, h.threads, uint32(len(h.key)))
	return subtle.ConstantTimeCompare(got, h.key) == 1
}

// Verify checks a password. It does the same work whether or not the account
// exists, and reports the account's groups on success.
func (u *Users) Verify(name, password string) (account string, groups []string, ok bool) {
	if u.observe != nil {
		defer u.observe()()
	}
	us, exists := u.byName[strings.ToLower(name)]
	if !exists {
		u.decoy.verify(password)
		return "", nil, false
	}
	if !us.hash.verify(password) {
		return "", nil, false
	}
	return us.name, us.groups, true
}

// Len is how many accounts the file holds.
func (u *Users) Len() int { return len(u.byName) }
