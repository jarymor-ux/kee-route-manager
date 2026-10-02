package update

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var repositoryRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}/[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)

type githubRelease struct {
	TagName    string        `json:"tag_name"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
	Assets     []githubAsset `json:"assets"`
}
type githubAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

func (u *Updater) githubManifestURLs(ctx context.Context) (string, string, string, error) {
	repo := u.cfg.GitHubRepository
	if !repositoryRE.MatchString(repo) {
		return "", "", "", fmt.Errorf("update.github_repository must be owner/repository")
	}
	// Search the recent 100 published releases. Drafts and Git tags without a
	// release cannot enter a channel. API listings remain untrusted; Check verifies
	// the manifest signature and binds its version to the chosen release tag.
	b, err := u.download(ctx, "https://api.github.com/repos/"+repo+"/releases?per_page=100", 4<<20)
	if err != nil {
		return "", "", "", err
	}
	var releases []githubRelease
	if err = json.Unmarshal(b, &releases); err != nil {
		return "", "", "", fmt.Errorf("decode GitHub releases: %w", err)
	}
	return releaseManifest(releases, repo, u.cfg.Channel)
}
func releaseManifest(rs []githubRelease, repo, channel string) (string, string, string, error) {
	var manifest, sig, tag string
	for _, r := range rs {
		version, err := parseSemVer(r.TagName)
		if err != nil || r.Draft || r.Prerelease != (len(version.pre) > 0) {
			continue
		}
		if channel == "rc" && !r.Prerelease || channel == "stable" && r.Prerelease {
			continue
		}
		if channel != "rc" && channel != "stable" {
			return "", "", "", fmt.Errorf("unsupported update channel")
		}
		prefix := "https://github.com/" + repo + "/releases/download/" + r.TagName + "/"
		name := "manifest-" + channel + ".json"
		m, s := "", ""
		for _, a := range r.Assets {
			if a.Name == name && a.URL == prefix+name {
				m = a.URL
			}
			if a.Name == name+".sig" && a.URL == prefix+name+".sig" {
				s = a.URL
			}
		}
		if m == "" || s == "" {
			continue
		}
		if tag == "" || compareVersions(strings.TrimPrefix(r.TagName, "v"), strings.TrimPrefix(tag, "v")) > 0 {
			manifest, sig, tag = m, s, r.TagName
		}
	}
	if tag == "" {
		return "", "", "", fmt.Errorf("no signed manifest published for channel %s among recent GitHub releases", channel)
	}
	return manifest, sig, tag, nil
}
