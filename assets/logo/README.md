# The pfwd logo

The mark is a tunnel, drawn the way the TUI and the web UI draw a forward. An open ring stands for the port on the server, a solid dot for where it arrives on your machine, and between them a packet sits on the line. Rising to the right, it reads as "from there to here".

## Files

| File | Use |
| --- | --- |
| `pfwd-horizontal.svg`, `pfwd-horizontal-dark.svg` | The default: symbol and name side by side. README headers, docs, slides. |
| `pfwd-stacked.svg`, `pfwd-stacked-dark.svg` | Symbol over the name, for square spaces. |
| `pfwd-symbol.svg`, `pfwd-symbol-dark.svg` | The symbol alone, from 48 px up. |
| `pfwd-symbol-small.svg`, `pfwd-symbol-small-dark.svg` | The small cut, under 48 px: no packet, a thicker line. Favicons, tabs, lists. |
| `pfwd-wordmark.svg`, `pfwd-wordmark-dark.svg` | The name alone, where the symbol already shows nearby. |
| `pfwd-app-icon.svg` | The small cut on the charcoal tile. |
| `social-preview.png` | GitHub's social preview, 1280 × 640. |

The `-dark` files are for dark backgrounds. The web UI's favicon (`web/src/favicon.svg`) is the small cut, and it picks light or dark from the page.

## Colors

| | Light backgrounds | Dark backgrounds |
| --- | --- | --- |
| Ink: the ring, the line, the name | `#16171A` | `#E9E9EC` |
| Green: the packet and the dot | `#1E7A45` | `#7DC88E` |
| Tile | | `#131416` |

The green is the UI's "active" color, so the logo's arriving dot is the same green as a working tunnel. Where only one color can print, draw everything in the ink.

## Type

The name is JetBrains Mono Bold, the UI's face, outlined, with letters about 5 % closer than the font sets them. JetBrains Mono is under the SIL Open Font License.

## Space and size

- Keep clear space around the mark of at least the dot's diameter.
- Use the small cut below 48 px. The full symbol's packet and thin line blur away at that size.
- Don't set the horizontal logo smaller than 24 px tall.

## Don't

- Don't recolor the ring or the line green, or the dot anything but the green.
- Don't straighten the tunnel, mirror it, or point it down: it always rises to the right.
- Don't add shadows, outlines or gradients, or set the name in another font.
