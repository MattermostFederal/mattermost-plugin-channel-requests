# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.3.1](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/compare/v0.3.0...v0.3.1) (2026-08-11)


### Bug Fixes

* **ci:** least-privilege secrets + correct SARIF ref on release call ([82c7f3a](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/82c7f3a3d9bb83cfeb54c816b54d2190ef29ac18))
* **ci:** publish release assets via reusable workflow ([56dc238](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/56dc238e407cea57d4e6c8fee4e7a527e70f430e))
* **ci:** publish release assets via reusable workflow ([0cc42d6](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/0cc42d6e5c1b3ba5ef82d658087ba63d25614080))
* correct stale fork references (alertmanager/template leftovers) ([c302569](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/c3025691b71e42b68bb1d49cd246355c131bf71b))
* correct stale fork references (alertmanager/template leftovers) ([15cbe50](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/15cbe5017889485a80fd05ced2d72cc6b066595a))


### Dependencies

* **actions:** bump the actions-minor-patch group with 4 updates ([60a63e3](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/60a63e3e40b06f09cf13d8aaf90a8d21d329d8bb))
* **npm:** bump the npm-minor-patch group in /webapp with 3 updates ([c32c27d](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/c32c27dc655a593fc9095ccb8b4b407fe7d0dc02))

## [0.3.0](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/compare/v0.2.1...v0.3.0) (2026-08-04)


### Features

* channel-admin requests (menu + Members-panel button) and orphaned-channel guard ([#15](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/issues/15)) ([fc6e85e](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/fc6e85e2370c0547d561225ed5586d18f0a40e49))


### Bug Fixes

* **deps:** bump Go deps to clear HIGH CVEs blocking the Grype gate ([c2ca69e](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/c2ca69e11e40ff59844baf545d63026e1e2c368a))
* harden channel-request auth/validation and unbreak the webapp toolchain ([#20](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/issues/20)) ([923647b](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/923647bae8a873a13dc00d8db2755d0d9febeb4b))


### Dependencies

* **actions:** bump actions/setup-go from 6.5.0 to 7.0.0 ([1f7b265](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/1f7b265bb59901d5cadb0b6523ddce5b94177523))
* **actions:** bump the actions-minor-patch group across 1 directory with 5 updates ([4031d52](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/4031d5216d87061e61269a59c4498631d5ac93e2))
* **actions:** Bump the actions-minor-patch group with 5 updates ([0e28b27](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/0e28b278cea1f433970e63cec86ed8b8202919c8))
* **npm:** bump the npm-minor-patch group across 1 directory with 13 updates ([711f9ff](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/711f9ff949eb536774783d8b89c06b98674ae575))
* **npm:** Bump the npm-minor-patch group in /webapp with 2 updates ([cb41d0c](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/cb41d0ca964ea206bb8c0e784864cab70b60d9da))
* **npm:** Bump typescript from 6.0.3 to 7.0.2 in /webapp ([c132307](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/c13230720b64a614afc1d04b8a5f43e0d4a14e5e))

## [0.2.1](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/compare/v0.2.0...v0.2.1) (2026-07-06)


### Bug Fixes

* **dependabot:** drop include:scope on go/npm so bumps land in changelogs ([4f1cf8d](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/4f1cf8d72ee99c6f33a2161d9b6ac93f440b3e6b))

## [0.2.0](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/compare/v0.1.0...v0.2.0) (2026-07-06)


### Features

* channel-admin request + tab-complete pickers + config cleanup ([8c0bb82](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/8c0bb827cc7ea888714efadabe43057eef996a12))
* click-ops system console + prefix editor + member picker ([672f0e3](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/672f0e312cbe9a0d6f030a57ebf4952a159bd27b))
* **webapp:** MemberPicker with avatars, badges, and MM-native sections ([f1c4b90](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/f1c4b90d79714051b7869ec2e00c101e82d72cfe))


### Bug Fixes

* **lint:** resolve golangci-lint findings so the lint gate passes ([22c6a7b](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/22c6a7bd6d65281838b2aa71a38ca13844524284))
* SearchUsers Limit=0 returns nothing, use explicit Limit=50 ([fc01236](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/fc012361c0c98a8b6a71d24b0991def0e4265cca))
* **webapp:** MemberPicker autocomplete now handles @-prefix + escapes modal clip ([0dd4367](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/0dd4367a7a6a80fe616cc64e9b0aabba8211f61d))


### Dependencies

* **actions:** Bump actions/setup-go from 5.5.0 to 6.5.0 ([c38c197](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/c38c19739a80ee323d8e9c20ea2d02d334bc8f4d))
* **actions:** Bump actions/upload-artifact from 4.4.3 to 7.0.1 ([9edbc92](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/9edbc92a847dbf20ff282176a9aef43c446369f4))
* **actions:** Bump anchore/scan-action from 5.2.0 to 7.4.0 ([ff3c366](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/ff3c366a08570493c236530f99b63870e1345038))
* **actions:** Bump googleapis/release-please-action from 4.2.0 to 5.0.0 ([09304a2](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/09304a2ac0451209596466a5df85ab06c8363e45))
* **actions:** Bump the actions-minor-patch group with 5 updates ([8146156](https://github.com/MattermostFederal/mattermost-plugin-channel-requests/commit/8146156e2a773ebeb5399f6f1454453d0c98563d))

## [Unreleased]

### Added
- Initial plugin template.
