# JIIX-Compatible RECOGNTEXT Injection Design

## Summary

UltraBridge (UB) already performs OCR on Supernote `.note` files and injects the resulting text back into the file's `RECOGNTEXT` block so the device can display and search it. The current implementation writes a minimal payload that the device cannot fully parse — leading to bugs such as injected text being silently discarded or overwritten after a sync. This design replaces that payload with one that is structurally identical to what the device itself produces: JIIX v3 "Raw Content" format, with proper word tokenization, space/newline separator entries, and bounding boxes in millimeters derived from the file's actual stroke geometry.

The work splits across two components. The go-sn format library gains a `BuildRecognText` function that owns all format knowledge: it tokenizes OCR text into JIIX word entries, computes a bounding box from `TOTALPATH` stroke data, and converts pixel coordinates to millimeters using per-device physical display dimensions. UltraBridge's worker pipeline gains two gating checks that run before injection: a check that the note uses real-time recognition (RTR) mode — the only mode where injection is meaningful — and a check that the device has already written its own recognition pass (all pages `RECOGNSTATUS=1`), re-queuing the job with a delay if not. Non-RTR notes still get OCR text indexed for full-text search; they are simply never modified on disk.

## Definition of Done

1. UB injects RECOGNTEXT in JIIX v3 "Raw Content" format indistinguishable in structure from device-native output
2. Bounding boxes computed from TOTALPATH stroke data, converted to mm per device model
3. RTR notes only: non-RTR notes get OCR + index but no file injection
4. UB waits for device recognition (RECOGNSTATUS=1 on all pages) before injecting; re-queues if not ready
5. Device displays UB's injected text in the recognition view and search results

## Acceptance Criteria

### jiix-recogntext.AC1: Injected RECOGNTEXT is valid JIIX
- **jiix-recogntext.AC1.1 Success:** Root type is "Raw Content" with elements array
- **jiix-recogntext.AC1.2 Success:** Text elements have type, label, and words fields
- **jiix-recogntext.AC1.3 Success:** No version, id, candidates, or reflow-label fields present
- **jiix-recogntext.AC1.4 Success:** Base64-encoded JSON round-trips correctly through InjectRecognText

### jiix-recogntext.AC2: Bounding boxes from stroke data
- **jiix-recogntext.AC2.1 Success:** StrokeBounds computes correct rect from stroke points
- **jiix-recogntext.AC2.2 Success:** Pixel bbox converted to mm using per-device physical dimensions
- **jiix-recogntext.AC2.3 Edge:** Empty stroke data (no TOTALPATH) produces zero-rect bbox
- **jiix-recogntext.AC2.4 Edge:** Unknown equipment string falls back to reasonable default or skips injection

### jiix-recogntext.AC3: Word tokenization matches device conventions
- **jiix-recogntext.AC3.1 Success:** Words split by whitespace, each gets shared bounding-box
- **jiix-recogntext.AC3.2 Success:** Spaces between words are `{"label":" "}` without bbox
- **jiix-recogntext.AC3.3 Success:** Newlines are `{"label":"\n"}` without bbox
- **jiix-recogntext.AC3.4 Success:** Trailing punctuation (. ! ? ,) becomes separate word with bbox
- **jiix-recogntext.AC3.5 Success:** Element label equals concatenation of all word labels
- **jiix-recogntext.AC3.6 Edge:** Empty text produces valid JIIX with no text elements

### jiix-recogntext.AC4: Non-RTR notes skip injection
- **jiix-recogntext.AC4.1 Success:** FILE_RECOGN_TYPE=0 note gets OCR + search index but no file modification
- **jiix-recogntext.AC4.2 Success:** Job completes as done (not failed)

### jiix-recogntext.AC5: Wait for device recognition
- **jiix-recogntext.AC5.1 Success:** All pages RECOGNSTATUS=1 proceeds to injection
- **jiix-recogntext.AC5.2 Failure:** Any page RECOGNSTATUS!=1 triggers re-queue

### jiix-recogntext.AC6: Re-queue with delay
- **jiix-recogntext.AC6.1 Success:** Re-queued job has requeue_after set in the future
- **jiix-recogntext.AC6.2 Success:** Worker skips jobs whose requeue_after hasn't passed
- **jiix-recogntext.AC6.3 Success:** Job is picked up after delay expires
- **jiix-recogntext.AC6.4 Edge:** Max retry count reached -> job marked failed with reason

### jiix-recogntext.AC7: Non-RTR indexing still works
- **jiix-recogntext.AC7.1 Success:** Non-RTR note text appears in search results
- **jiix-recogntext.AC7.2 Success:** Non-RTR note file is not modified on disk

## Glossary

- **JIIX**: JSON Interactive Ink eXchange — MyScript's JSON schema for representing handwriting recognition output. UltraBridge targets JIIX v3 "Raw Content" mode, which represents recognized text as an array of text elements each containing a flat list of word-level entries.
- **RECOGNTEXT**: A named block inside a Supernote `.note` file that stores recognition output. Its value is a base64-encoded JIIX JSON payload. The device reads this block to power on-device search and the recognition text view.
- **RECOGNSTATUS**: A per-page metadata field in `.note` files. A value of `1` means the device has completed its own handwriting recognition pass for that page. UltraBridge waits for this before injecting, to avoid a race with the device's recognizer.
- **FILE_RECOGN_TYPE**: A file-level metadata field in `.note` files indicating whether real-time recognition is enabled. Value `"1"` means RTR mode is active; other values indicate the device's recognizer is not running and injection would be meaningless.
- **RTR (Real-Time Recognition)**: A Supernote operating mode in which the device's on-device MyScript engine recognizes handwriting as the user writes and stores results in `RECOGNTEXT`. Only RTR notes have a meaningful recognition layer to augment.
- **TOTALPATH**: A layer in `.note` page data that stores all pen strokes as coordinate sequences. UltraBridge decodes this to compute a bounding rectangle covering the written text region.
- **Bounding box**: A rectangle (x, y, width, height) in millimeters that locates a word or text element on the page. The device uses bounding boxes for search result highlighting. In this design, all words on a page share a single bbox covering the full stroke extent.
- **go-sn**: The Go library that implements `.note` file parsing, rendering, and modification. It owns all format-level knowledge (block layout, RECOGNTEXT injection, device metadata). `BuildRecognText` and `StrokeBounds` are added to this library.
- **InjectRecognText**: An existing go-sn function that handles the binary mechanics of writing a `RECOGNTEXT` block into a `.note` file — base64 encoding, block placement, offset updates, setting `RECOGNSTATUS=1`. This function is unchanged; only the `RecognContent` value it receives changes.
- **RecognContent**: A Go struct in go-sn representing the top-level JIIX payload passed to `InjectRecognText`. Extended here to support the `"Raw Content"` root type with a `words` field on elements.
- **APPLY_EQUIPMENT**: A metadata field in `.note` files identifying the device model (e.g., `"N6"`, `"A5X"`). Used by go-sn to look up pixel dimensions and, in this design, physical display dimensions for pixel-to-mm conversion.
- **SPC (Supernote Private Cloud)**: The self-hosted sync server that replicates `.note` files between the device and cloud storage. Changes injected by UltraBridge propagate to the device via SPC sync.
- **requeue_after**: A new column added to the UltraBridge jobs table in SQLite. When the device has not yet completed recognition, the job is re-scheduled by setting this timestamp to a future time. The worker skips jobs until the delay expires.
- **MyScript**: The third-party handwriting recognition engine embedded in Supernote devices. It defines the JIIX format and produces the `RECOGNTEXT` payloads that UltraBridge emulates.

## Architecture

Two components change: go-sn (format library) and UltraBridge's worker pipeline.

**go-sn** gains responsibility for constructing device-compatible JIIX from plain OCR text and stroke geometry. It already handles RECOGNTEXT injection at the binary level (`InjectRecognText`); this extends it to also build the JIIX payload. go-sn owns format knowledge — tokenization rules, mm conversion, JIIX structure — so callers don't need to understand MyScript internals.

**UltraBridge worker** gains two gating checks before injection (RTR detection, device recognition readiness) and switches from manually constructing `RecognContent` to calling go-sn's builder. The render/OCR/index pipeline is unchanged.

### Data flow (per page)

```
TOTALPATH stroke data ──► DecodeObjects() ──► StrokeBounds() ──► pixel Rect
                                                                      │
OCR plain text ─────────────────────────────────────────────┐         │
                                                            ▼         ▼
                                                    BuildRecognText(text, bounds, equipment)
                                                            │
                                                            ▼
                                                    RecognContent (JIIX "Raw Content")
                                                            │
                                                            ▼
                                                    InjectRecognText(pageIdx, content)
                                                            │
                                                            ▼
                                                    modified .note bytes ──► write to disk
```

### JIIX target format

The device produces JIIX v3 "Raw Content" mode. UB's output must be structurally identical:

```json
{
  "type": "Raw Content",
  "elements": [
    {
      "type": "Text",
      "label": "Full text including\nnewlines",
      "words": [
        {"bounding-box": {"x": 10.1, "y": 10.8, "width": 90.0, "height": 50.0}, "label": "Full"},
        {"label": " "},
        {"bounding-box": {"x": 10.1, "y": 10.8, "width": 90.0, "height": 50.0}, "label": "text"},
        {"label": "\n"},
        {"bounding-box": {"x": 10.1, "y": 10.8, "width": 90.0, "height": 50.0}, "label": "newlines"}
      ]
    }
  ]
}
```

All words share a single bounding box computed from the TOTALPATH stroke extent, converted from pixels to mm. The device uses this for search result highlighting — a shared bbox means any search hit highlights the entire text region. This is acceptable; text correctness is the priority.

Fields the device omits (and UB must also omit): `version`, `id`, `bounding-box` at root/element level, `candidates`, `reflow-label`.

### Gating logic

```
load .note file
  │
  ├── FILE_RECOGN_TYPE != "1" ──► OCR + index only, skip injection, job = done
  │
  ├── any page RECOGNSTATUS != "1" ──► re-queue with delay (requeue_after column)
  │
  └── all pages ready ──► render/OCR/inject/index as normal
```

### Pixel-to-mm conversion

JIIX bounding boxes are in millimeters. Stroke data is in portrait pixel space. Conversion requires physical display dimensions per device model.

go-sn already maps `APPLY_EQUIPMENT` to pixel dimensions (`devicePortraitWidth`/`devicePortraitHeight`). This extends to include physical mm dimensions. Conversion per axis: `mm = px * physicalMM / pixelDim`.

Known devices:
| Equipment | Pixels | Display | Physical approx |
|---|---|---|---|
| N6 (Nomad) | 1404x1872 | 7.8" | ~119x159mm |
| A5X | 1404x1872 | 10.3" | ~157x210mm |
| Manta | 1920x2560 | 10.67" | ~146x194mm |

Exact physical dimensions should be derived from diagonal size and pixel aspect ratio rather than marketing DPI numbers.

### Word tokenization rules

Derived from analysis of 20 device-recognized pages (documented in `/mnt/supernote/FINDINGS.md`):

- Split on whitespace to get words
- Each word gets its own entry with shared bounding-box
- Spaces between words: `{"label": " "}` — no bounding-box
- Newlines between lines: `{"label": "\n"}` — no bounding-box
- Punctuation at word boundaries (`.`, `!`, `?`, `,`) becomes a separate entry with bounding-box
- Element-level `label` is the exact concatenation of all word labels (including spaces and newlines)
- All text goes in a single `"Text"` element (no multi-block splitting — we don't have the spatial information to segment)

## Existing Patterns

### go-sn injection

`InjectRecognText(pageIdx int, content RecognContent)` already handles the binary work: base64-encoding JSON, writing the block, updating page metadata offsets, setting RECOGNSTATUS=1, and handling multi-page offset relocation. This function stays unchanged — only the `RecognContent` it receives changes.

### go-sn device lookups

`devicePortraitWidth`/`devicePortraitHeight` in `parse.go` already map equipment strings to pixel dimensions. Adding physical mm dimensions follows the same pattern.

### UltraBridge worker pipeline

The per-page loop in `worker.go` (render → OCR → inject → reload → index) stays structurally identical. The injection step changes from building `RecognContent` manually to calling `BuildRecognText`. The two new gates (RTR check, RECOGNSTATUS check) add early returns before the page loop.

### SQLite migrations

`internal/notedb/` handles schema migrations with sequential version numbers. Adding `requeue_after` to the jobs table follows the existing migration pattern.

## Implementation Phases

<!-- START_PHASE_1 -->
### Phase 1: go-sn JIIX Builder

**Goal:** go-sn can construct device-compatible JIIX RecognContent from plain text and stroke geometry.

**Components:**
- `StrokeBounds(strokes []Stroke) Rect` in `note/totalpath.go` — computes axis-aligned bounding box of all strokes
- `devicePhysicalMM(equipment string) (float64, float64)` in `note/parse.go` — physical display dimensions lookup alongside existing pixel dims
- `BuildRecognText(text string, strokeBounds Rect, equipment string) RecognContent` in `note/write.go` — tokenizes text, converts bbox pixels→mm, builds JIIX "Raw Content" structure
- `RecognWord` struct in `note/write.go` — word-level entry with `Label` and optional `BoundingBox`
- `Words []RecognWord` field on `RecognElement` (JSON tag: `"words"`)
- Updated `RecognContent` to support `"Raw Content"` root type

**Dependencies:** None

**Covers:** jiix-recogntext.AC1 (JIIX format), jiix-recogntext.AC2 (bounding boxes), partially jiix-recogntext.AC3 (tokenization)

**Done when:** Tests verify that `BuildRecognText` produces structurally valid JIIX matching device conventions: correct root type, proper word tokenization with space/newline separators, bounding boxes in mm, element label matching word concatenation. `StrokeBounds` correctly computes bounding rect. Pixel-to-mm conversion produces values in the expected range for known devices.
<!-- END_PHASE_1 -->

<!-- START_PHASE_2 -->
### Phase 2: UltraBridge Gating and Re-queue

**Goal:** UB only injects into RTR notes with completed device recognition, re-queuing when not ready.

**Components:**
- RTR gate in `internal/processor/worker.go` — checks `FILE_RECOGN_TYPE == "1"`, skips injection for non-RTR notes (still OCR + index)
- RECOGNSTATUS gate in `internal/processor/worker.go` — checks all pages have `RECOGNSTATUS == "1"`, triggers re-queue if not
- `requeue_after` column on jobs table — SQLite migration in `internal/notedb/`
- Worker loop update to respect `requeue_after` (skip jobs whose `requeue_after` is in the future)
- Max retry count to prevent infinite re-queuing

**Dependencies:** Phase 1 (go-sn types must exist for compilation, though gating logic is independent)

**Covers:** jiix-recogntext.AC4 (RTR-only injection), jiix-recogntext.AC5 (wait for device recognition), jiix-recogntext.AC6 (re-queue), jiix-recogntext.AC7 (non-RTR indexing)

**Done when:** Tests verify: non-RTR notes skip injection but complete OCR+index; RTR notes with incomplete recognition are re-queued with delay; re-queued jobs are picked up after delay expires; max retries prevent infinite loops; jobs with all pages recognized proceed to injection.
<!-- END_PHASE_2 -->

<!-- START_PHASE_3 -->
### Phase 3: Integration — Wire BuildRecognText into Worker

**Goal:** UB's injection pipeline produces device-compatible JIIX using go-sn's builder.

**Components:**
- Updated injection call in `internal/processor/worker.go` — replaces manual `RecognContent` construction with `BuildRecognText(text, strokeBounds, equipment)`
- Stroke bounds computation added to per-page loop (decode TOTALPATH → `StrokeBounds`)

**Dependencies:** Phase 1 (BuildRecognText), Phase 2 (gating logic)

**Covers:** jiix-recogntext.AC1 through AC7 (end-to-end)

**Done when:** Full pipeline test: RTR note with device recognition → UB processes → injected RECOGNTEXT is valid JIIX with correct text, proper word structure, mm bounding boxes. Non-RTR notes still index correctly without injection. Existing tests continue to pass.
<!-- END_PHASE_3 -->

## Additional Considerations

**SPC sync interaction:** The device won't re-recognize pages with `RECOGNSTATUS=1` (user confirmed: re-recognition requires explicit button press). By waiting for device recognition first and then injecting proper JIIX format, SPC sync should propagate UB's better text to the device without triggering re-recognition. This was the root cause of the Cocktails.note bug — UB's old format was unreadable by the device, possibly triggering re-recognition.

**Bounding box accuracy:** All words share a single text-region bbox. Search result highlighting shows a rectangle around all text on the page rather than individual words. This is a deliberate trade-off: text correctness matters, highlight precision does not (confirmed by user testing on device).

**Device physical dimensions:** The mm conversion uses computed physical dimensions (from display diagonal and pixel aspect ratio) rather than DPI numbers. If a new device model appears, go-sn needs an entry in the device lookup tables. Unknown equipment strings should fall back to a reasonable default or skip injection with a warning.
