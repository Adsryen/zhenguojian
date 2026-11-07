package core

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func decodeSourcesCommand(t *testing.T, body string) (map[string]any, error) {
	t.Helper()
	var envelope struct {
		OK    bool           `json:"ok"`
		Data  map[string]any `json:"data"`
		Error string         `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("listSources/setSourceEnabled replied invalid JSON: %v", err)
	}
	if !envelope.OK {
		return nil, errors.New(envelope.Error)
	}
	return envelope.Data, nil
}

func TestProviderRegistryCanonicalParity(t *testing.T) {
	// The migrated domain/alias table must normalize exactly like the old
	// central switch, including the TrimSpace fallback for unknown values.
	cases := map[string]string{
		"huangguo": sourceHuangguoAI, "huangguoai": sourceHuangguoAI, "HUANGGUOAI.COM": sourceHuangguoAI,
		"huangguo-video": sourceHuangguoVideo, "huangguo.video": sourceHuangguoVideo,
		"huangdou": sourceHuangdou, "tideember.cc": sourceHuangdou, "xqjurgek.top": sourceHuangdou,
		"hongguo": sourceHongguo, "hongguoduanju.com": sourceHongguo,
		"huangju": sourceHuangju, "api.huangju.net": sourceHuangju,
		"yeguo": sourceYeguo, "delta.ygrwdsgt.cc": sourceYeguo, "www.yeguodj.com": sourceYeguo,
		"dsd": sourceDSD, "www.dsd.com.se": sourceDSD,
		"cloudfront": sourceCloudFront,
		"sorani": sourceSorani, "sorani.cc": sourceSorani, "api.sorani.cc": sourceSorani,
		" heitaodj.com ": "heitao", "jennyhow.com": "hanxiaoquan", "guipianwu.com": "guipian",
		"jgdj.app": "jinguoduanju", "hghts.rlkfus.cn": "jinguo",
		"  Hongguo  ": sourceHongguo,
		"Unknown-Thing": "Unknown-Thing", "  spaced value ": "spaced value",
	}
	for raw, want := range cases {
		if got := canonicalProviderSource(raw); got != want {
			t.Fatalf("canonicalProviderSource(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestProviderRegistryListAndToggle(t *testing.T) {
	setSourcePrefsOverrides(nil)
	t.Cleanup(func() { setSourcePrefsOverrides(nil) })

	body := NativeSourcesCommand(`{"action":"listSources"}`)
	data, err := decodeSourcesCommand(t, body)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(data["items"])
	var items []nativeSourceListItem
	if json.Unmarshal(raw, &items) != nil {
		t.Fatal("listSources items invalid")
	}
	if len(items) < 14 {
		t.Fatalf("registry lists %d sources, want >=14", len(items))
	}
	bySlug := map[string]nativeSourceListItem{}
	for _, item := range items {
		bySlug[item.Slug] = item
	}
	hongguo := bySlug[sourceHongguo]
	if hongguo.DisplayName != "红果" || !hongguo.Implemented || !hongguo.Enabled || !hongguo.Seasons {
		t.Fatalf("hongguo entry wrong: %+v", hongguo)
	}
	guipian := bySlug["guipian"]
	if guipian.DisplayName != "鬼片" || !guipian.Implemented || !guipian.Enabled || guipian.SearchKind != providerSearchRSS {
		t.Fatalf("ported source must be implemented and enabled: %+v", guipian)
	}

	// Toggle yeguo off then on through the command surface.
	if _, err := decodeSourcesCommand(t, NativeSourcesCommand(`{"action":"setSourceEnabled","slug":"www.yeguodj.com","enabled":false}`)); err != nil {
		t.Fatal(err)
	}
	if err := nativeAuthorizeInput(nativeInput{Action: "catalog", Source: sourceYeguo}); !errors.Is(err, errSourceDisabled) {
		t.Fatalf("disabled source not rejected: %v", err)
	}
	if _, err := decodeSourcesCommand(t, NativeSourcesCommand(`{"action":"setSourceEnabled","slug":"yeguo","enabled":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := nativeAuthorizeInput(nativeInput{Action: "catalog", Source: sourceYeguo}); err != nil {
		t.Fatalf("re-enabled source still rejected: %v", err)
	}

	// Error surface: unknown slug and unimplemented slug (the envelope
	// round-trip keeps the message, not the sentinel identity).
	if err := func() error {
		_, e := decodeSourcesCommand(t, NativeSourcesCommand(`{"action":"setSourceEnabled","slug":"nope","enabled":true}`))
		return e
	}(); err == nil || !strings.Contains(err.Error(), "未知站源") {
		t.Fatalf("unknown slug accepted: %v", err)
	}
}

func TestSourcePrefsPersistenceRoundTrip(t *testing.T) {
	setSourcePrefsOverrides(nil)
	t.Cleanup(func() { setSourcePrefsOverrides(nil) })
	directory := t.TempDir()
	engine := &nativeEngine{directory: directory}
	nativeState.Lock()
	previous := nativeState.engine
	nativeState.engine = engine
	nativeState.Unlock()
	t.Cleanup(func() {
		nativeState.Lock()
		nativeState.engine = previous
		nativeState.Unlock()
	})

	if _, err := decodeSourcesCommand(t, NativeSourcesCommand(`{"action":"setSourceEnabled","slug":"huangdou","enabled":false}`)); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filepath.Join(directory, "source_prefs.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stored), "\"huangdou\":false") {
		t.Fatalf("prefs file content wrong: %s", stored)
	}
	setSourcePrefsOverrides(nil)
	if !providerSourceEnabled(sourceHuangdou) {
		t.Fatal("cleared overrides should restore factory default")
	}
	restored := &nativeEngine{directory: directory}
	restored.loadSourceRecords()
	if providerSourceEnabled(sourceHuangdou) {
		t.Fatal("restart lost the stored disable switch")
	}
	if err := nativeAuthorizeInput(nativeInput{Action: "catalog", Source: sourceHuangdou}); !errors.Is(err, errSourceDisabled) {
		t.Fatalf("restored switch not enforced: %v", err)
	}
}
