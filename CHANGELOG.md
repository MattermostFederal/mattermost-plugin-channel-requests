# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
