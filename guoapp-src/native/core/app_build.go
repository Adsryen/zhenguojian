package core

import "errors"

// buildAllSources is a build-time force-all override for the factory level:
// when "true", registered+implemented sources are enabled even if their
// EnabledByDefault is false. Explicit user preferences in source_prefs.json
// still win. The provider registry is the source of truth for what ships
// enabled by default; zhenguojian keeps all sources on without this flag.
var buildAllSources = "false"

var errNativeBuildSource = errors.New("当前版本不包含此站源")

var errSourceDisabled = errors.New("该站源已在设置中停用")

// nativeSourceGateError distinguishes the two ways a source can be
// unavailable: not part of this build (missing implementation) versus
// switched off by the user in source_prefs.json.
func nativeSourceGateError(source string) error {
	source = canonicalProviderSource(source)
	def, ok := providerSourceDef(source)
	if !ok || !def.Implemented {
		return errNativeBuildSource
	}
	if !providerSourceEnabled(def.Slug) {
		return errSourceDisabled
	}
	return nil
}

func nativeSourceAvailable(source string) bool {
	return nativeSourceGateError(source) == nil
}

func nativeDramaAvailable(drama nativeDrama) bool {
	source, _, valid := splitProviderDramaID(drama.ID)
	return valid && nativeSourceAvailable(source) &&
		(drama.Source == "" || canonicalProviderSource(drama.Source) == source)
}

func nativeChapterAvailable(drama nativeDrama, chapter Chapter) bool {
	if !nativeDramaAvailable(drama) {
		return false
	}
	source, _, _ := splitProviderDramaID(drama.ID)
	if chapter.Source != "" && canonicalProviderSource(chapter.Source) != source {
		return false
	}
	if chapterSource, _, valid := splitProviderDramaID(chapter.ID); valid && chapterSource != source {
		return false
	}
	return true
}

func nativeDownloadAvailable(job nativeDownloadJob) bool {
	return nativeChapterAvailable(job.Drama, job.Chapter)
}

func nativeAuthorizeInput(input nativeInput) error {
	switch input.Action {
	case "recommendations", "cachedRecommendations", "suggestions", "danmaku":
		if err := nativeSourceGateError(sourceHongguo); err != nil {
			return err
		}
	case "rankings":
		board, found := findRankingBoard(input.Board)
		if !found {
			return errNativeBuildSource
		}
		if err := nativeSourceGateError(board.Source); err != nil {
			return err
		}
	case "soraniAccount":
		if err := nativeSourceGateError(sourceSorani); err != nil {
			return err
		}
	case "catalog", "cached", "categories", "sourceStatus", "sourceJob", "cancelSourceJob":
		if err := nativeSourceGateError(input.Source); err != nil {
			return err
		}
		if input.Action == "sourceJob" && input.Drama.ID != "" &&
			(!nativeDramaAvailable(input.Drama) || sourceFromDramaID(input.Drama.ID) != canonicalProviderSource(input.Source)) {
			return errNativeBuildSource
		}
	case "cover", "prepareCover", "detail", "metadata", "resolve", "preload", "prepareHandoff", "enqueueDownloads", "localPlayback":
		if !nativeDramaAvailable(input.Drama) {
			if source, _, valid := splitProviderDramaID(input.Drama.ID); valid {
				if err := nativeSourceGateError(source); err != nil {
					return err
				}
			}
			return errNativeBuildSource
		}
		if (input.Action == "resolve" || input.Action == "preload" || input.Action == "prepareHandoff") && !nativeChapterAvailable(input.Drama, input.Chapter) {
			return errNativeBuildSource
		}
	}
	return nil
}
