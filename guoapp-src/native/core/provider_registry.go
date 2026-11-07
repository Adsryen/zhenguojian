package core

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
)

// ProviderDef is the factory descriptor of one provider source. Every
// provider implementation file registers its def from init(); sources whose
// implementation has not landed yet register with Implemented=false so the
// registry stays authoritative for slugs, display names and normalization
// while availability logic keeps them hidden from the user.
type ProviderDef struct {
	Slug             string   // canonical id, e.g. "hongguo", "huangguo-video"
	DisplayName      string   // user-facing name, e.g. "红果"
	SearchKind       string   // providerSearchOnline / providerSearchCache / providerSearchRSS
	Lanes            bool     // playback exposes multiple lines (线路/sid/lineCode)
	Seasons          bool     // search merges "第N季" seasons
	CatalogKinds     []string // hardcoded catalog kind ids (empty = remote or single list)
	EnabledByDefault bool     // factory enablement when the user has no preference
	Domains          []string // extra alias keys (domains) normalizing to Slug
	Implemented      bool     // provider code exists in this build
}

const (
	providerSearchOnline = "online"
	providerSearchCache  = "cache"
	providerSearchRSS    = "rss"
)

type providerRegistryItem struct {
	def     ProviderDef
	domains []string
}

var (
	providerRegistryMu    sync.RWMutex
	providerRegistryItems = map[string]*providerRegistryItem{}
	// providerAliasIndex maps lower(trim(alias|domain)) -> canonical slug.
	// Aliases (including every slug itself and its domains) must be unique
	// across defs; on accidental collision the last registration wins.
	providerAliasIndex = map[string]string{}
)

// registerProviderSource adds or merges a def by slug. Merging is monotonic:
// Implemented/Lanes/Seasons/EnabledByDefault only ever turn true, text and
// kind fields are overwritten when the new registration supplies them, and
// domain aliases accumulate. This makes init() ordering between an
// implementation file and a placeholder registration irrelevant.
func registerProviderSource(def ProviderDef) {
	def.Slug = strings.ToLower(strings.TrimSpace(def.Slug))
	if def.Slug == "" {
		return
	}
	providerRegistryMu.Lock()
	defer providerRegistryMu.Unlock()
	item := providerRegistryItems[def.Slug]
	if item == nil {
		item = &providerRegistryItem{}
		providerRegistryItems[def.Slug] = item
	}
	merged := item.def
	merged.Slug = def.Slug
	if strings.TrimSpace(def.DisplayName) != "" {
		merged.DisplayName = strings.TrimSpace(def.DisplayName)
	}
	if def.SearchKind != "" {
		merged.SearchKind = def.SearchKind
	}
	merged.Lanes = merged.Lanes || def.Lanes
	merged.Seasons = merged.Seasons || def.Seasons
	merged.EnabledByDefault = merged.EnabledByDefault || def.EnabledByDefault
	merged.Implemented = merged.Implemented || def.Implemented
	merged.CatalogKinds = appendUniqueStrings(merged.CatalogKinds, def.CatalogKinds)
	merged.Domains = appendUniqueStrings(merged.Domains, def.Domains)
	item.def = merged
	if item.domains == nil {
		item.domains = []string{}
	}
	item.domains = appendUniqueStrings(item.domains, def.Domains)
	providerAliasIndex[def.Slug] = def.Slug
	for _, domain := range def.Domains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain != "" {
			providerAliasIndex[domain] = def.Slug
		}
	}
}

// providerSourceDef returns the merged def for an exact canonical slug.
func providerSourceDef(slug string) (ProviderDef, bool) {
	slug = strings.ToLower(strings.TrimSpace(slug))
	providerRegistryMu.RLock()
	defer providerRegistryMu.RUnlock()
	item, ok := providerRegistryItems[slug]
	if !ok {
		return ProviderDef{}, false
	}
	return item.def, true
}

// providerSourceDefs lists every registered def sorted by slug for stable
// enumeration (listSources, settings UI).
func providerSourceDefs() []ProviderDef {
	providerRegistryMu.RLock()
	defs := make([]ProviderDef, 0, len(providerRegistryItems))
	for _, item := range providerRegistryItems {
		defs = append(defs, item.def)
	}
	providerRegistryMu.RUnlock()
	sortProviderDefs(defs)
	return defs
}

func sortProviderDefs(defs []ProviderDef) {
	for i := 1; i < len(defs); i++ {
		for j := i; j > 0 && defs[j].Slug < defs[j-1].Slug; j-- {
			defs[j], defs[j-1] = defs[j-1], defs[j]
		}
	}
}

// providerSlugForAlias resolves a raw alias (slug, domain or display id) that
// was registered by some def. The second return is false for unknown keys.
func providerSlugForAlias(key string) (string, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return "", false
	}
	providerRegistryMu.RLock()
	defer providerRegistryMu.RUnlock()
	slug, ok := providerAliasIndex[key]
	return slug, ok
}

// isRegisteredProviderSource reports whether the (possibly raw) source
// normalizes to a registered provider slug.
func isRegisteredProviderSource(source string) bool {
	_, ok := providerSourceDef(canonicalProviderSource(source))
	return ok
}

// providerSourceImplemented reports whether a registered source has provider
// code in this build (def registered but implementation missing = false).
func providerSourceImplemented(source string) bool {
	def, ok := providerSourceDef(canonicalProviderSource(source))
	return ok && def.Implemented
}

// sourcePrefsOverrides holds user enable/disable choices (slug -> enabled).
// It is loaded from source_prefs.json by app_sources.go once the engine has a
// data directory; while empty, factory defaults apply.
var sourcePrefsOverrides struct {
	mu      sync.RWMutex
	enabled map[string]bool
}

func setSourcePrefsOverrides(overrides map[string]bool) {
	sourcePrefsOverrides.mu.Lock()
	defer sourcePrefsOverrides.mu.Unlock()
	copyOverrides := make(map[string]bool, len(overrides))
	for slug, enabled := range overrides {
		copyOverrides[strings.ToLower(strings.TrimSpace(slug))] = enabled
	}
	sourcePrefsOverrides.enabled = copyOverrides
}

func setSourcePrefsOverride(slug string, enabled bool) {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		return
	}
	sourcePrefsOverrides.mu.Lock()
	defer sourcePrefsOverrides.mu.Unlock()
	if sourcePrefsOverrides.enabled == nil {
		sourcePrefsOverrides.enabled = map[string]bool{}
	}
	sourcePrefsOverrides.enabled[slug] = enabled
}

func sourcePrefsOverride(slug string) (bool, bool) {
	sourcePrefsOverrides.mu.RLock()
	defer sourcePrefsOverrides.mu.RUnlock()
	enabled, ok := sourcePrefsOverrides.enabled[slug]
	return enabled, ok
}

// providerSourceEnabled is the single availability decision for one canonical
// slug: user preference first, then the build-wide force-all flag, then the
// factory EnabledByDefault from the registry.
func providerSourceEnabled(slug string) bool {
	def, ok := providerSourceDef(slug)
	if !ok || !def.Implemented {
		return false
	}
	if enabled, found := sourcePrefsOverride(slug); found {
		return enabled
	}
	if buildAllSources == "true" {
		return true
	}
	return def.EnabledByDefault
}

func appendUniqueStrings(base []string, extra []string) []string {
	for _, value := range extra {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		found := false
		for _, existing := range base {
			if existing == value {
				found = true
				break
			}
		}
		if !found {
			base = append(base, value)
		}
	}
	return base
}

// init registers the five sources whose provider implementation has not
// landed yet (Q subtasks add the implementation files and re-register these
// slugs with Implemented=true; the merge keeps that flag sticky).
func init() {
	registerProviderSource(ProviderDef{
		Slug: "jinguoduanju", DisplayName: "禁果短剧", SearchKind: providerSearchOnline,
		EnabledByDefault: true, Domains: []string{"jinguoduanju.app", "jgdj.app", "play.jinguoduanju.app"},
	})
	registerProviderSource(ProviderDef{
		Slug: "jinguo", DisplayName: "禁果", SearchKind: providerSearchOnline,
		EnabledByDefault: true, Domains: []string{"d1wyuxwdrlykj1.cloudfront.net", "hghts.rlkfus.cn"},
	})
	registerProviderSource(ProviderDef{
		Slug: "heitao", DisplayName: "黑桃", SearchKind: providerSearchOnline, Lanes: true,
		EnabledByDefault: true, Domains: []string{"heitaodj.com"},
	})
	registerProviderSource(ProviderDef{
		Slug: "guipian", DisplayName: "鬼片", SearchKind: providerSearchRSS, Lanes: true,
		EnabledByDefault: true, Domains: []string{"guipianwu.com"},
	})
	registerProviderSource(ProviderDef{
		Slug: "hanxiaoquan", DisplayName: "韩小圈", SearchKind: providerSearchOnline, Lanes: true,
		EnabledByDefault: true, Domains: []string{"jennyhow.com", "www.jennyhow.com"},
	})
}

// nativeSourcePrefsFile is the on-disk shape of source_prefs.json, stored
// next to sources.json in the engine data directory.
type nativeSourcePrefsFile struct {
	Overrides map[string]bool `json:"overrides"`
}

func sourcePrefsSnapshot() map[string]bool {
	sourcePrefsOverrides.mu.RLock()
	defer sourcePrefsOverrides.mu.RUnlock()
	snapshot := make(map[string]bool, len(sourcePrefsOverrides.enabled))
	for slug, enabled := range sourcePrefsOverrides.enabled {
		snapshot[slug] = enabled
	}
	return snapshot
}

// nativeSourceListItem is one entry of the listSources payload: the factory
// descriptor plus the effective enablement state the UI renders.
type nativeSourceListItem struct {
	Slug             string   `json:"slug"`
	DisplayName      string   `json:"displayName"`
	SearchKind       string   `json:"searchKind"`
	Lanes            bool     `json:"lanes"`
	Seasons          bool     `json:"seasons"`
	CatalogKinds     []string `json:"catalogKinds"`
	EnabledByDefault bool     `json:"enabledByDefault"`
	Implemented      bool     `json:"implemented"`
	Enabled          bool     `json:"enabled"`
}

func nativeSourcesEnvelope(data any, err error) string {
	if err != nil {
		body, marshalErr := json.Marshal(map[string]any{"ok": false, "error": err.Error()})
		if marshalErr != nil {
			return `{"ok":false,"error":"本地核心处理失败，请重试"}`
		}
		return string(body)
	}
	body, marshalErr := json.Marshal(map[string]any{"ok": true, "data": data})
	if marshalErr != nil {
		return `{"ok":false,"error":"本地核心处理失败，请重试"}`
	}
	return string(body)
}

// NativeSourcesCommand serves the registry-level line-protocol commands
// "listSources" and "setSourceEnabled" for host shells (zhenguojian sidecar
// dispatches them here; guoapp bridge may route them the same way later).
// The response uses the same {"ok":…,"data":…}/{"ok":false,"error":…}
// envelope as NativeRequest.
func NativeSourcesCommand(raw string) string {
	var request struct {
		Action  string `json:"action"`
		Slug    string `json:"slug"`
		Enabled *bool  `json:"enabled"`
	}
	if json.Unmarshal([]byte(raw), &request) != nil {
		return nativeSourcesEnvelope(nil, errors.New("请求格式无效"))
	}
	switch request.Action {
	case "listSources":
		defs := providerSourceDefs()
		items := make([]nativeSourceListItem, 0, len(defs))
		for _, def := range defs {
			kinds := def.CatalogKinds
			if kinds == nil {
				kinds = []string{}
			}
			items = append(items, nativeSourceListItem{
				Slug: def.Slug, DisplayName: def.DisplayName, SearchKind: def.SearchKind,
				Lanes: def.Lanes, Seasons: def.Seasons,
				CatalogKinds: kinds, EnabledByDefault: def.EnabledByDefault,
				Implemented: def.Implemented, Enabled: providerSourceEnabled(def.Slug),
			})
		}
		return nativeSourcesEnvelope(map[string]any{"items": items}, nil)
	case "setSourceEnabled":
		if request.Enabled == nil {
			return nativeSourcesEnvelope(nil, errors.New("请求格式无效"))
		}
		slug := canonicalProviderSource(request.Slug)
		def, ok := providerSourceDef(slug)
		if !ok {
			return nativeSourcesEnvelope(nil, errors.New("未知站源"))
		}
		if !def.Implemented {
			return nativeSourcesEnvelope(nil, errNativeBuildSource)
		}
		setSourcePrefsOverride(slug, *request.Enabled)
		nativeState.Lock()
		engine := nativeState.engine
		nativeState.Unlock()
		if engine != nil {
			engine.mu.Lock()
			err := engine.saveSourcePrefsLocked()
			engine.mu.Unlock()
			if err != nil {
				return nativeSourcesEnvelope(nil, err)
			}
		}
		return nativeSourcesEnvelope(map[string]any{
			"slug": slug, "enabled": *request.Enabled, "persisted": engine != nil,
		}, nil)
	default:
		return nativeSourcesEnvelope(nil, errors.New("不支持的应用操作"))
	}
}
