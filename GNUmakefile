# Tracked local-deploy helper. GNU make reads GNUmakefile before Makefile, so
# this file includes the real Makefile and adds deploy-local. CI and contributors
# using GNU make pick this up; BSD make still uses Makefile alone.
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
