package cc

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"runtime"
	"sync"
	"time"

	"cmd2api/internal/id"
)

type Fingerprint struct {
	Thumbmark  string           `json:"thumbmark"`
	Components FingerprintParts `json:"components"`
}

type FingerprintParts struct {
	MachineIDHash    string   `json:"machineIdHash"`
	MACHashes        []string `json:"macHashes"`
	OSUserHash       string   `json:"osUserHash"`
	HostnameHash     string   `json:"hostnameHash"`
	GitEmailHash     string   `json:"gitEmailHash"`
	Platform         string   `json:"platform"`
	Arch             string   `json:"arch"`
	OSRelease        string   `json:"osRelease"`
	CPUModel         string   `json:"cpuModel"`
	CPUCount         int      `json:"cpuCount"`
	MemGiB           int      `json:"memGiB"`
	IsContainer      bool     `json:"isContainer"`
	Timezone         string   `json:"timezone"`
	Runtime          string   `json:"runtime"`
	CollectorVersion int      `json:"collectorVersion"`
}

type keyState struct {
	fp         Fingerprint
	nextInitAt time.Time
	sessionID  string
	sessionExp time.Time
}

type StateStore struct {
	mu   sync.Mutex
	keys map[string]*keyState
}

func NewStateStore() *StateStore {
	return &StateStore{keys: map[string]*keyState{}}
}

func (s *StateStore) Session(apiKey, hint string) string {
	if len(hint) >= 8 {
		return hint
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.get(apiKey)
	now := time.Now()
	if st.sessionID == "" || now.After(st.sessionExp) {
		st.sessionID = id.New()
		st.sessionExp = now.Add(12*time.Hour + jitter(time.Hour))
	}
	return st.sessionID
}

func (s *StateStore) NeedInit(apiKey string) (Fingerprint, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.get(apiKey)
	if time.Now().Before(st.nextInitAt) {
		return st.fp, false
	}
	return st.fp, true
}

func (s *StateStore) MarkInit(apiKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.get(apiKey)
	st.nextInitAt = time.Now().Add(8*time.Hour + jitter(2*time.Hour))
}

func (s *StateStore) get(apiKey string) *keyState {
	st, ok := s.keys[apiKey]
	if !ok {
		st = &keyState{fp: GenerateFingerprint()}
		s.keys[apiKey] = st
	}
	return st
}

func GenerateFingerprint() Fingerprint {
	cpus := []struct {
		model string
		cores int
	}{
		{"12th Gen Intel(R) Core(TM) i7-12650H", 10},
		{"13th Gen Intel(R) Core(TM) i7-13700K", 16},
		{"AMD Ryzen 7 7800X3D", 8},
		{"AMD Ryzen 9 7950X", 16},
	}
	mems := []int{16, 24, 32, 64}
	tzs := []string{"America/New_York", "Europe/Berlin", "Asia/Shanghai", "Asia/Tokyo"}

	cpu := cpus[randN(len(cpus))]
	macN := 2 + randN(3)
	macs := make([]string, macN)
	for i := range macs {
		macs[i] = shaHex(randBytes(32))
	}
	machine := shaHex(randBytes(32))
	user := shaHex(randBytes(16))
	host := shaHex(randBytes(16))
	git := shaHex(randBytes(16))
	mem := mems[randN(len(mems))]
	joined := fmt.Sprintf("%s|%s|%s|%s|win32|10.0.22631|%s|%d|%d",
		machine, user, host, git, cpu.model, cpu.cores, mem)
	for _, m := range macs {
		joined += "|" + m
	}

	return Fingerprint{
		Thumbmark: shaHex([]byte(joined)),
		Components: FingerprintParts{
			MachineIDHash:    machine,
			MACHashes:        macs,
			OSUserHash:       user,
			HostnameHash:     host,
			GitEmailHash:     git,
			Platform:         "win32",
			Arch:             "x64",
			OSRelease:        "10.0.22631",
			CPUModel:         cpu.model,
			CPUCount:         cpu.cores,
			MemGiB:           mem,
			Timezone:         tzs[randN(len(tzs))],
			Runtime:          "cli",
			CollectorVersion: 1,
		},
	}
}

func Environment() string {
	return fmt.Sprintf("%s-%s, Go", runtime.GOOS, runtime.GOARCH)
}

func shaHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

func randN(n int) int {
	if n <= 0 {
		return 0
	}
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0
	}
	return int(v.Int64())
}

func jitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	return time.Duration(randN(int(max)))
}
