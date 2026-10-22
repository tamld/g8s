---
name: g8s-ux-visual-gate
description: "Headless browser DOM bounding-box collision, computed typography/contrast, console runtime error, keyboard navigation, and media playback contract verifier."
domain: verification
version: 1.0.0
last_updated: "2026-10-22"
risk_level: medium
evidence_level: runtime_required
args_syntax: "g8s verify-ux --url <target_url> [--viewport <WxH>] [--wcag-level <AA|AAA>] [--media-selector <sel>] [--json]"
---

# g8s UX & Visual Gate Verification Skill

The `g8s-ux-visual-gate` skill provides automated headless browser verification of front-end user experience, visual layout geometry, accessibility (a11y), runtime stability, and media streaming contracts.

---

## 1. Purpose & Threat Defeated

### Threats Defeated
- **Visual Regressions & Collisions**: Silent CSS regressions causing overlapping text, button occlusion, clipped containers, and unexpected responsive wrapping.
- **Layout Overlaps & Clipping**: Dynamic DOM sizing where elements overflow bounds or have negative z-index/clipping masks rendering them unclickable or illegible.
- **Inaccessible Low Contrast**: Poor color combinations violating WCAG contrast ratios, disproportionately impacting elderly users and users in varied lighting conditions.
- **Broken Keyboard Navigation**: Focus traps, missing `tabindex` flows, invisible focus indicators (`outline: none`), and unnavigable modal dialogs.
- **Uncaught Frontend Runtime Errors**: Silent JavaScript exceptions, unhandled Promise rejections, and network asset 404s logging to the browser console without failing superficial end-to-end tests.
- **Audio/Video Playback Stalls**: Media elements stuck in buffer states, stalled playback pipelines, broken autoplay policies, or unplayable stream sources.

### Mechanism
The gate runs a headless Chromium/WebKit instance against target pages, evaluating DOM metrics directly:
1. **Geometry Inspection**: Calls `getBoundingClientRect()` across interactive and typographic nodes to detect geometric intersection and occlusion.
2. **Computed Style Analysis**: Queries `window.getComputedStyle()` to compute luminance and contrast ratios between text and background surfaces against WCAG AA (4.5:1 / 3:1) and AAA (7:1 / 4.5:1) standards.
3. **Accessibility Traversal**: Simulates keyboard `Tab`, `Shift+Tab`, `Enter`, and `Space` sequences, verifying sequential element focusability and focus ring visibility.
4. **Console & Network Trap**: Hooks `window.console.error`, `window.console.warn`, and `window.onerror`, treating any uncaught page error as an automatic failure.
5. **Media Pipeline Assertions**: Hooks HTMLMediaElement events (`playing`, `stalled`, `error`, `waiting`) to verify media playback starts within deterministic thresholds and sustains uninterrupted rendering.

---

## 2. Deterministic Inputs

| Input | Description | Format / Default |
|---|---|---|
| **Target URL / Endpoint** | Live local or staging server endpoint to verify. | `http://localhost:<port>/<path>` |
| **Viewport Dimensions** | Width and height matrices for multi-screen responsive checks. | `--viewport 1280x800` (e.g., `375x667`, `768x1024`, `1920x1080`) |
| **WCAG Contrast Threshold** | Accessibility standard compliance level. | `AA` (default) or `AAA` |
| **Keyboard Focus Sequence** | Key sequence or target selector sequence for accessibility traversal. | Array of CSS selectors or default document root |
| **Media Playback Selectors** | Selectors for video or audio elements under test. | CSS selector (e.g. `video#primary-stream`, `audio.player`) |
| **Settling Timeout** | Maximum time in milliseconds to wait for network idle and animations. | `--settle-ms 2000` |

---

## 3. Execution Commands

### Run Comprehensive UX Verification
Verify visual bounding boxes, typography, keyboard focus, and console errors against a local endpoint:
```bash
g8s verify-ux \
  --url "http://127.0.0.1:8080/dashboard" \
  --viewport "1280x800" \
  --wcag-level "AA" \
  --json
```

### Multi-Viewport Responsive Matrix
Run the visual gate across mobile, tablet, and desktop viewports:
```bash
for vp in "375x667" "768x1024" "1920x1080"; do
  g8s verify-ux \
    --url "http://127.0.0.1:8080/dashboard" \
    --viewport "$vp" \
    --json >> ux-verdict-"$vp".json
done
```

### Media Stream & Audio/Video Contract Verification
Validate media playback, stream continuity, and buffer health:
```bash
g8s verify-ux \
  --url "http://127.0.0.1:8080/player" \
  --media-selector "video#hls-player" \
  --playback-duration "5s" \
  --json
```

### Headless Browser Runner Direct Invocation
Run the underlying Playwright/Puppeteer verification probe script directly:
```bash
node tools/ux_verifier.js \
  --target "http://127.0.0.1:8080" \
  --out "$G8S_STATE_DIR/evidence/ux-report.json"
```

---

## 4. JSON Verdict Schema

The verification output produces a machine-readable JSON verdict complying with the following schema:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "UXVisualGateVerdict",
  "type": "object",
  "required": [
    "verdict",
    "bounding_box_overlaps",
    "contrast_failures",
    "console_errors",
    "keyboard_navigable",
    "media_playback_ok"
  ],
  "properties": {
    "verdict": {
      "type": "string",
      "enum": ["PASS", "FAIL"]
    },
    "bounding_box_overlaps": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["element_a", "element_b", "intersection_area_px"],
        "properties": {
          "element_a": { "type": "string", "description": "CSS selector or tag identifier for first element" },
          "element_b": { "type": "string", "description": "CSS selector or tag identifier for overlapping element" },
          "intersection_area_px": { "type": "number", "description": "Area of bounding box intersection in square pixels" }
        }
      }
    },
    "contrast_failures": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["selector", "actual_ratio", "required_ratio", "fg_color", "bg_color"],
        "properties": {
          "selector": { "type": "string" },
          "actual_ratio": { "type": "number" },
          "required_ratio": { "type": "number" },
          "fg_color": { "type": "string" },
          "bg_color": { "type": "string" }
        }
      }
    },
    "console_errors": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["type", "message"],
        "properties": {
          "type": { "type": "string", "enum": ["error", "warning", "unhandled_rejection"] },
          "message": { "type": "string" },
          "source_url": { "type": "string" },
          "line_number": { "type": "integer" }
        }
      }
    },
    "keyboard_navigable": {
      "type": "boolean",
      "description": "True if all focusable nodes can be navigated via Tab without focus traps or invisible outlines"
    },
    "media_playback_ok": {
      "type": "boolean",
      "description": "True if specified media elements played smoothly without stall or autoplay block"
    }
  }
}
```

### Example Verdict Output
```json
{
  "verdict": "PASS",
  "bounding_box_overlaps": [],
  "contrast_failures": [],
  "console_errors": [],
  "keyboard_navigable": true,
  "media_playback_ok": true
}
```

### Example Failing Verdict Output
```json
{
  "verdict": "FAIL",
  "bounding_box_overlaps": [
    {
      "element_a": "button#submit-btn",
      "element_b": "div.floating-toast",
      "intersection_area_px": 142.5
    }
  ],
  "contrast_failures": [
    {
      "selector": "p.footer-disclaimer",
      "actual_ratio": 2.8,
      "required_ratio": 4.5,
      "fg_color": "#888888",
      "bg_color": "#ffffff"
    }
  ],
  "console_errors": [
    {
      "type": "error",
      "message": "Uncaught TypeError: Cannot read properties of undefined (reading 'token')",
      "source_url": "http://127.0.0.1:8080/assets/app.js",
      "line_number": 142
    }
  ],
  "keyboard_navigable": false,
  "media_playback_ok": true
}
```

---

## 5. F0 -> F1 -> F2 Delegation Contract

```
[ F0 Supervisor ]
       |
       | 1. Dispatches F1 Worker with target UI/UX acceptance criteria
       | 2. Provides headless browser test harnesses in repo tools
       v
[ F1 Worker (wt-xxx) ]
       |
       | 1. Starts local ephemeral test server in isolated worktree
       | 2. Authors HTML/CSS/JS/Frontend code
       | 3. May invoke F2 Subagent to triage specific layout bugs
       v
[ F2 Subagent (Triage/Fix) ]
       | Inspects DOM geometry, fixes CSS/JS within F1's scope
       | Reports verified fixes back to F1
       v
[ F1 Worker (wt-xxx) ]
       | Runs `g8s verify-ux` locally against ephemeral server
       | Collects screenshot / JSON verdict evidence
       | Stops ephemeral server; submits sealed receipt
       v
[ F0 Supervisor ]
       | Verifies evidence JSON: bounding_box_overlaps == 0,
       | console_errors == 0, contrast_failures == 0,
       | keyboard_navigable == true, media_playback_ok == true.
       | Grants acceptance.
```

- **F0 Supervisor Authority**: Defines required WCAG levels, supported viewports, and zero-console-error gating criteria. F0 accepts the task only upon reviewing the verified JSON verdict.
- **F1 Worker Boundary**: Spawns isolated local dev servers bound to loopback (`127.0.0.1`) on random ephemeral ports. F1 collects headless browser test evidence and attaches the resulting `UXVisualGateVerdict` JSON to the work receipt.
- **F2 Subagent Containment**: Subagents triage and fix styling or component behavior strictly within the worktree files allowed by the active Write Receipt.
