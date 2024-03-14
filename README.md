# 🗑️

🗑️ is a simple pastebin.

Go to https://🗑️.st or https://waste.st and type or paste things.

## API

```
echo hello world | curl -T - 🗑️.st
```

See https://🗑️.st/waste.1 for more details.

## Design

This aims to be a no-framework, fast loading site, while being modern.

Everything is served as a single HTML page, the aim is to keep it (gzip
compressed) under ~10K for the front page load.

Important files:
  * [w.go](w.go) contains the main go code;
  * [w.html](w.html) contains a Go template which has the HTML and JavaScript.
