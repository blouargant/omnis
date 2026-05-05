# internal/filter

This folder contains the minimal bash-output filtering subsystem imported from the snip project.

## Provenance

The following files were copied and adapted from snip to keep integration scope narrow:

- actions.go
- parser.go
- pipeline.go
- registry.go
- regex.go
- types.go
- utils.go

The following files were added in agent-toolkit to wire this subsystem into the current runtime and tests:

- loader.go
- match.go
- filter_test.go
- test_helpers_test.go

## Upstream Project

- Project: snip
- Upstream repository: https://github.com/edouard-claude/snip/
- Copied upstream license file in this folder: LICENSE

## Original Project License (snip)

snip is licensed under the MIT License.
See LICENSE in this folder for the copied upstream license text.

## Notes

- This folder is intentionally limited to bash output filtering logic.
- Non-filter snip subsystems (CLI, tracking, trust, discovery, learning, executor wrapper) were not imported.
