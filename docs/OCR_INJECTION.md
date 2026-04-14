# Standalone OCR Injection with sninject

UltraBridge's Supernote OCR pipeline runs automatically when notes
change on the device. If you need to process a `.note` file outside
the pipeline — to debug a specific injection, test OCR quality on a
single file, or one-off process a file from backup — the
[go-sn](https://github.com/jdkruzr/go-sn) library ships a standalone
`sninject` tool that does the same work without involving
UltraBridge's database, sync, or watcher.

```bash
go install github.com/jdkruzr/go-sn/cmd/sninject@latest

# Process a note using the same vLLM endpoint as UltraBridge
sninject -in original.note -out processed.note \
  -api-url http://192.168.9.199:8000 \
  -model Qwen/Qwen3-VL-8B-Instruct

# Dry run — see OCR results without modifying the file
sninject -in original.note -out /dev/null -dry-run
```

See the [go-sn README](https://github.com/jdkruzr/go-sn#sninject)
for full usage.

## Why `-zero-recognfile`?

When UltraBridge (or `sninject`) injects RECOGNTEXT into an RTR note,
the device's MyScript engine detects a mismatch between the injected
text and its own RECOGNFILE (iink recognition data). On the next
sync, the device re-runs recognition from RECOGNFILE and overwrites
the injected text — often with lower quality results (especially for
math, symbols, and measurements).

Zeroing RECOGNFILE removes the data the device uses to re-derive
RECOGNTEXT, preventing this clobbering. The trade-off: if you later
add new strokes to that page on the device, it may need to do a full
recognition pass instead of an incremental update.

## When not to use sninject

If you're sitting in front of UltraBridge and the file is already
being watched, the standard pipeline is both easier and produces the
same output: enable OCR in Settings, then hit **Queue** or **Force**
on the file from the Supernote Files tab. `sninject` is for the
case where you *can't* (or don't want to) run the full pipeline.
