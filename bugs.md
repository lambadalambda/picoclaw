# Bugs Checklist

All issues in this checklist are now fixed and covered by regression tests.

- [x] `pkg/cron/service.go`: Stop->Start restart works (`pkg/cron/service_bug_test.go`)
- [x] `pkg/heartbeat/service.go`: Start works on fresh service and after restart (`pkg/heartbeat/service_test.go`)
- [x] `pkg/bus/bus.go`: Close-safe publish/consume semantics (no panic after close; closed reads return `ok=false`) (`pkg/bus/bus_bug_test.go`)
- [x] `pkg/channels/manager.go`: `StartAll()` idempotent; no leaked dispatchers after repeated start/stop (`pkg/channels/manager_test.go`)
- [x] `pkg/tools/cron.go`: nil executor path handled without panic (`pkg/tools/cron_test.go`)
