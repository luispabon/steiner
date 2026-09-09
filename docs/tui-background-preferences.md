# TUI background preferences

Steiner persists TUI preferences in `~/.config/steiner/prefs.yaml`. `sidebar_bg` controls the sidebar surface and `content_bg` controls seamless main-pane content. Quote YAML colors because `#` starts a comment:

```yaml
sidebar_bg: "#000000"
content_bg: "#0a0a0a"
```

Missing or empty values retain defaults. Non-empty values must be `#RRGGBB`; valid values are canonicalized to lowercase. Invalid colors are rejected. Backgrounds remain selected when the accent preset is rebuilt.