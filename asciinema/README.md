# Demo recording

`demo.svg` embedded in the main README is a **simulation**: `demo.sh` types out
real `nova` commands for pacing, but the output blocks are canned text, not a
live run. A real instance create + app install takes several minutes (instance
provisioning, image build and push); this takes seconds and needs no
instance, registry login or credentials.

The canned output was written to match the actual CLI's message formats
(checked against `pkg/cmd/instance.go` and `pkg/cmd/app.go` at the time this
was written), but it is not re-verified automatically, so it can drift from
reality as the CLI changes. If you touch the messages this demo imitates
(`nova instance create`, `nova app create`, `nova app install`, `nova app
list`), check `demo.sh`'s canned blocks still match.

Regenerate after editing `demo.sh`:

```bash
asciinema/record.sh
```

Colors come from `@wandelbots/design-tokens` (the same package `hack/docsite`
vendors): the prompt is a truecolor ANSI escape set to
`--wb-zero-gravity-primary-main` (`#8e56fc`) in `demo.sh`, and `record.sh`
post-processes svg-term's background fill to
`--wb-zero-gravity-background-paper-elevation-1` (`#141623`). Bump both if the
design tokens package bumps those values.
