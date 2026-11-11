package updater

import (
	"encoding/json"
	"testing"
)

const releasesFixture = `[
 {"tag_name":"v1.5.0","prerelease":false,"published_at":"2026-01-01T00:00:00Z","body":"old v1",
  "assets":[{"name":"zhenGuoJian-windows-x64.zip","browser_download_url":"https://example.com/v1.zip","size":11}]},
 {"tag_name":"v2.0.0-alpha.3","prerelease":true,"published_at":"2026-02-01T00:00:00Z","body":"pre notes",
  "assets":[{"name":"zhenGuoJian-v2.exe","browser_download_url":"https://example.com/a3.exe","size":21000000},
            {"name":"build-info.json","browser_download_url":"https://example.com/a3-info.json","size":120}]},
 {"tag_name":"v2.0.0","prerelease":false,"published_at":"2026-02-20T00:00:00Z","body":"stable notes",
  "assets":[{"name":"zhenGuoJian-v2.exe","browser_download_url":"https://example.com/s.exe","size":21000001},
            {"name":"zhenGuoJian-v2-runtime.zip","browser_download_url":"https://example.com/rt.zip","size":3000}]},
 {"tag_name":"nightly-2026","prerelease":true,"published_at":"2026-03-01T00:00:00Z","body":"junk",
  "assets":[{"name":"zhenGuoJian-v2.exe","browser_download_url":"https://example.com/n.exe","size":1}]},
 {"tag_name":"v2.1.0-alpha.1","prerelease":true,"published_at":"2026-03-02T00:00:00Z","body":"next pre",
  "assets":[{"name":"zhenGuoJian-v2-app.zip","browser_download_url":"https://example.com/n21.zip","size":2}]}
]`

func TestParseReleasesFilters(t *testing.T) {
	rels, err := ParseReleases([]byte(releasesFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 3 {
		t.Fatalf("want 3 v2 releases (v1.* 与 junk tag 剔除)，got %d: %+v", len(rels), tagsOf(rels))
	}
	for _, r := range rels {
		if !IsV2Release(r) {
			t.Errorf("%s should be v2-marked", r.Tag)
		}
	}
}

func TestPickLatest(t *testing.T) {
	rels, err := ParseReleases([]byte(releasesFixture))
	if err != nil {
		t.Fatal(err)
	}
	r := PickLatest(rels, false)
	if r == nil || r.Tag != "v2.0.0" {
		t.Errorf("stable channel pick=%v want v2.0.0", tagOrNil(r))
	}
	r = PickLatest(rels, true)
	if r == nil || r.Tag != "v2.1.0-alpha.1" {
		t.Errorf("prerelease channel pick=%v want v2.1.0-alpha.1", tagOrNil(r))
	}
	onlyPre := []Release{{Tag: "v2.0.0-alpha.3", Prerelease: true,
		Assets: []Asset{{Name: DefaultExeAsset}}}}
	if PickLatest(onlyPre, false) != nil {
		t.Error("stable channel must not pick prerelease-only list")
	}
}

func TestReleaseAssets(t *testing.T) {
	rels, _ := ParseReleases([]byte(releasesFixture))
	var a3 *Release
	for i := range rels {
		if rels[i].Tag == "v2.0.0-alpha.3" {
			a3 = &rels[i]
		}
	}
	a, err := a3.FindAsset("")
	if err != nil || a.Name != DefaultExeAsset {
		t.Errorf("default asset lookup failed: %+v %v", a, err)
	}
	if _, err := a3.FindAsset("zhenGuoJian-v2-runtime.zip"); err == nil {
		t.Error("alpha.3 has no runtime asset, expect error")
	}
	sel := a3.SelectableAssets()
	if len(sel) != 1 || sel[0].Name != DefaultExeAsset {
		t.Errorf("SelectableAssets should hide build-info.json: %+v", sel)
	}
}

func TestReleasesRoundTripJSON(t *testing.T) {
	rels, _ := ParseReleases([]byte(releasesFixture))
	data, err := json.Marshal(rels[1])
	if err != nil {
		t.Fatal(err)
	}
	var back Release
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Tag != rels[1].Tag || back.Assets[0].Size != rels[1].Assets[0].Size {
		t.Errorf("round trip mismatch: %+v", back)
	}
}

func tagOrNil(r *Release) string {
	if r == nil {
		return "<nil>"
	}
	return r.Tag
}

func tagsOf(rels []Release) []string {
	out := []string{}
	for _, r := range rels {
		out = append(out, r.Tag)
	}
	return out
}
