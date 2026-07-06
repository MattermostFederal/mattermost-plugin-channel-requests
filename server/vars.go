package main

// RepoURL is the canonical GitHub URL for this plugin's source, used by
// Go code that needs to render links into the repository. The default
// below is the upstream fallback; the Makefile overrides it at compile
// time via:
//
//	-ldflags "-X 'github.com/MattermostFederal/mattermost-plugin-channel-requests/server.RepoURL=<resolved-url>'"
//
// where <resolved-url> comes from $GITHUB_REPOSITORY in CI or `git
// remote get-url origin` locally. Lets fork/move/rename of the repo
// flow into the binary without source edits.
//
// Kept as a package-level var (not read from Manifest) because the
// embedded manifest in a Mattermost plugin is generated from the
// source plugin.json — which now carries __PLUGIN_REPO_URL__ placeholders
// that only get substituted at bundle time. Using this var sidesteps
// the placeholder leak into Go-visible surfaces.
var RepoURL = "https://github.com/MattermostFederal/mattermost-plugin-channel-requests"
