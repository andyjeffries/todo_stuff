# TodoStuff Brand

A calm, self-hosted todo manager. The visual identity is paper-feeling and quiet — soft sand, sage greens, a confident tick. The mark is built from three offset list cards; the front card has one item ticked. It speaks directly to the GTD-style smart lists (Today / Upcoming / Logbook) at the heart of the app.

---

## Files

All assets live under `brand/`. Sizes are square unless noted.

### Vector — `brand/svg/`

| File | Purpose |
|---|---|
| `todostuff-icon.svg` | Master icon. Paper-sand background, full color. Use this for app art, large hero placements, OG images. |
| `todostuff-icon-transparent.svg` | Same icon, no background plate. Drop on any surface (white, dark UI chrome, photographs). |
| `todostuff-icon-monochrome.svg` | Single-tone fallback (no background). For monochrome contexts: print, embossing, terminal screenshots, single-color merch. |
| `todostuff-icon-maskable.svg` | PWA maskable variant — the design is inset to 80% so OS-level cropping (Android adaptive icons, iOS rounded squares) does not chop the cards. |
| `todostuff-wordmark-ink.svg` | "TodoStuff" wordmark in Outfit SemiBold, outlined to paths (no font dependency). Ink (`#1d1d1b`) — for use on light surfaces. |
| `todostuff-wordmark-light.svg` | Same wordmark, paper-cream (`#fbf6ea`) — for use on dark surfaces. |
| `todostuff-logo-ink.svg` | Full logo lockup (icon + wordmark) for light backgrounds — full-color icon + ink wordmark. |
| `todostuff-logo-light.svg` | Full logo lockup for dark backgrounds — transparent-bg icon + cream wordmark. |

### Raster — `brand/png/`

| File | Use |
|---|---|
| `todostuff-icon-16.png` | Browser tab favicon fallback. |
| `todostuff-icon-32.png` | Browser tab / Windows taskbar fallback. |
| `todostuff-icon-48.png` | Windows taskbar high-DPI. |
| `todostuff-icon-64.png` | macOS dock at 1×, GitHub README headers. |
| `todostuff-icon-96.png` | Android home screen (mdpi). |
| `todostuff-icon-128.png` | Mac dock 2×, store listings. |
| `todostuff-icon-180.png` / `apple-touch-icon.png` | iOS home screen. |
| `todostuff-icon-192.png` | Android Chrome PWA. |
| `todostuff-icon-256.png` | Windows Modern apps. |
| `todostuff-icon-512.png` | PWA splash, app stores, README. |
| `todostuff-icon-1024.png` | macOS app icon, App Store, large hero use. |
| `todostuff-icon-maskable-192.png` / `…-512.png` | PWA `purpose: "maskable"` icons. |
| `todostuff-icon-transparent-256.png` / `…-512.png` | Transparent rasters for embedding. |
| `todostuff-social-1200x630.png` | Open Graph / Twitter card. |

### Other

| File | Use |
|---|---|
| `brand/favicon.ico` | Multi-size .ico (16/32/48). Drop at the web root. |

---

## Wordmark — Google Font recommendation

The app currently uses no display typeface for the brand name. After trying several options against the mark, the recommendation is:

### **Outfit**

[Outfit on Google Fonts](https://fonts.google.com/specimen/Outfit) — by Rodrigo Fuenzalida & Smartsheet. Geometric but slightly humanist; pairs naturally with the rounded card geometry of the icon. Free, weights 100–900, full Latin Extended.

- Wordmark weight: **600 (SemiBold)**
- Tracking: **-0.01em** at display sizes
- Pair with **Inter** at 400/500 for body UI (Inter is also what Tailwind's default font stack favors, so it composes cleanly with HTMX-rendered HTML).

```html
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Outfit:wght@400;500;600;700&family=Inter:wght@400;500;600&display=swap" rel="stylesheet">
```

```css
.brand-wordmark { font-family: 'Outfit', system-ui, sans-serif; font-weight: 600; letter-spacing: -0.01em; color: #1d1d1b; }
body { font-family: 'Inter', system-ui, sans-serif; }
```

> **Note:** the wordmark SVGs (`todostuff-wordmark-*.svg`, `todostuff-logo-*.svg`) have the text outlined to paths, so they render correctly even on systems where Outfit isn't installed. Use the SVGs for any in-app display of the wordmark; only load the Google Font when you're rendering the wordmark as live HTML text (where it can be selected, translated, etc).

---

## Color tokens

```css
:root {
  /* Backgrounds */
  --ts-bg-top:        #f6f1e6;  /* paper top */
  --ts-bg-bot:        #e3dccb;  /* paper bottom */
  --ts-bg:            #efe9de;  /* solid fallback */

  /* Card surfaces */
  --ts-card-back:     #d8cfbe;
  --ts-card-mid:      #e9e1cf;
  --ts-card-front:    #fbf6ea;
  --ts-rule:          #c2b9a4;

  /* Ink + content */
  --ts-ink:           #1d1d1b;
  --ts-ink-soft:      #3b342a;
  --ts-muted:         #6c6a63;
  --ts-border:        #3b342a;

  /* Brand greens */
  --ts-ring:          #9bbca5;  /* light sage — checkbox rings (checked AND unchecked) */
  --ts-tick:          #2f5e3c;  /* deep forest — the “done” mark */
  --ts-row-done:      #c2b9a4;  /* completed-row text color (de-emphasized) */
}
```

---

## Applying the brand to the app

The icon and these tokens give you a small, cohesive system. Concrete suggestions for `web/templates/` and `web/static/css/app.css`:

### 1. Wire up the favicon family

In your base layout `<head>`:

```html
<link rel="icon" href="/static/favicon.ico" sizes="any">
<link rel="icon" type="image/svg+xml" href="/static/brand/todostuff-icon.svg">
<link rel="apple-touch-icon" href="/static/brand/apple-touch-icon.png">
<link rel="manifest" href="/static/manifest.webmanifest">
<meta name="theme-color" content="#efe9de">
```

`manifest.webmanifest`:

```json
{
  "name": "TodoStuff",
  "short_name": "TodoStuff",
  "start_url": "/",
  "display": "standalone",
  "background_color": "#efe9de",
  "theme_color": "#efe9de",
  "icons": [
    { "src": "/static/brand/todostuff-icon-192.png", "sizes": "192x192", "type": "image/png" },
    { "src": "/static/brand/todostuff-icon-512.png", "sizes": "512x512", "type": "image/png" },
    { "src": "/static/brand/todostuff-icon-maskable-192.png", "sizes": "192x192", "type": "image/png", "purpose": "maskable" },
    { "src": "/static/brand/todostuff-icon-maskable-512.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable" }
  ]
}
```

This crosses one item off your roadmap (favicon) and quietly preps the PWA story you mention as future work.

### 2. Soften the app shell

The mark suggests a paper-feeling chrome rather than a stark white shell. In Tailwind v4, extend the theme:

```css
@theme {
  --color-paper:       #efe9de;
  --color-paper-soft:  #f6f1e6;
  --color-card:        #fbf6ea;
  --color-ink:         #1d1d1b;
  --color-ink-soft:    #3b342a;
  --color-muted:       #6c6a63;
  --color-rule:        #c2b9a4;
  --color-ring:        #9bbca5;
  --color-tick:        #2f5e3c;
}
```

Then: `body` → `bg-paper text-ink`. Sidebar → `bg-paper-soft`. Task list cards → `bg-card border border-ink-soft/10`. Hairlines → `border-rule`.

### 3. Use the two-tone green consistently

This is the defining brand move. Apply it everywhere completion is signaled:

- **Checkbox ring** (checked or unchecked): `--ts-ring` (`#9bbca5`)
- **Check mark glyph**: `--ts-tick` (`#2f5e3c`)
- **Completed task row title**: `--ts-row-done` (de-emphasized cream-grey)

The Logbook view should feel like a sea of these light rings with dark ticks inside.

### 4. Accent for status

Re-purpose the brand greens as the only saturated colors in the UI. Reserve them for completion / success states and the "today" indicator. Use `--ts-ink-soft` for important flags (the star) instead of yellow — keep the palette tight.

For overdue, use a warm rust drawn from the same paper family: **`#a85432`**. It harmonizes with the sand without introducing a new hue family.

### 5. Wordmark lockup

When the icon and "TodoStuff" appear together (sidebar header, login screen, README):

- Icon height = wordmark cap-height × 1.6
- Gap between icon and word = wordmark cap-height × 0.5
- Vertical-align the wordmark optical center to the icon center, not the baseline

```html
<a class="brand" href="/">
  <img src="/static/brand/todostuff-icon-128.png" alt="" width="40" height="40">
  <span>TodoStuff</span>
</a>
```

```css
.brand { display: inline-flex; align-items: center; gap: 12px; }
.brand img { width: 40px; height: 40px; border-radius: 9px; }
.brand span { font-family: 'Outfit', sans-serif; font-weight: 600; font-size: 22px;
              letter-spacing: -0.01em; color: var(--ts-ink); }
```

### 6. Empty states & loading

Use the transparent monochrome SVG, large and at low opacity (8–12%), as a watermark on empty list states ("Inbox is empty", "No tasks for today"). Better than a generic illustration and keeps the brand consistent.

### 7. Don't

- Don't recolor the tick. The two-tone green hierarchy *is* the brand.
- Don't skew, rotate, or 3D the icon further — the cards already have a slight fan.
- Don't place the full-color mark on a saturated background; switch to the transparent or monochrome variant.
- Don't add a glow, drop-shadow, or bevel. The mark is meant to feel like paper, not chrome.

---

## Quick checklist for shipping the rebrand

- [ ] Drop `brand/favicon.ico` and `brand/png/*` into `web/static/`
- [ ] Add the `<link rel="icon">` tags + `manifest.webmanifest`
- [ ] Add the `Outfit` + `Inter` Google Fonts link to your base layout
- [ ] Apply the color tokens to `app.css` (or as Tailwind theme vars)
- [ ] Update the sidebar lockup to use the new icon + wordmark
- [ ] Repaint task-row checkboxes with `--ts-ring` and `--ts-tick`
- [ ] Set OG image meta tags to `/static/brand/todostuff-social-1200x630.png`
