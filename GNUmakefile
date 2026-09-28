# Local-only convenience — NOT committed. GNU make reads GNUmakefile before
# Makefile, so this file just include's the real Makefile and adds one target.
# Delete it to remove; it changes nothing in the tracked build.
#
# `make deploy-local` mirrors the alertmanager plugin: build the bundle and
# upload it to an ALREADY-RUNNING Mattermost over the API (no docker managed
# here). Same requirement as alertmanager — set MM_ADMIN_TOKEN in your env
# (a sysadmin personal access token on the target server):
#
#   export MM_ADMIN_TOKEN=<token>
#   make deploy-local                                     # → http://localhost:8065
#   make deploy-local MM_SITEURL=http://localhost:8066    # a different server
#
# pluginctl also accepts MM_ADMIN_USERNAME + MM_ADMIN_PASSWORD instead of a
# token. Uses build/bin/pluginctl (compiled on demand if missing).

include Makefile

MM_SITEURL ?= http://localhost:8065
PLUGINCTL  := ./build/bin/pluginctl

.PHONY: deploy-local
deploy-local: dist
	@test -x $(PLUGINCTL) || $(GO) build -o $(PLUGINCTL) ./build/pluginctl
	MM_SERVICESETTINGS_SITEURL=$(MM_SITEURL) $(PLUGINCTL) deploy $(PLUGIN_ID) dist/$(BUNDLE_NAME)
