// Package assets embeds the static images and fonts used by the stats card.
// Embedding keeps the runtime image free of filesystem dependencies.
package assets

import _ "embed"

// StatsCard is the clean background of the /stats card (1672x941 PNG).
//
//go:embed stickers/stats/card.png
var StatsCard []byte

// StatsFont is DejaVu Sans Bold, used for the dynamic text on the card. The
// font carries accented Portuguese characters and the masculine ordinal.
//
//go:embed fonts/DejaVuSans-Bold.ttf
var StatsFont []byte

// Extra fonts used by the rich renderer for glyphs DejaVu does not carry.
// They are OFL-licensed; see fonts/LICENSE-Noto-AND-STIX.txt.
//
//go:embed fonts/NotoSansSinhala.ttf
var SinhalaFont []byte

//go:embed fonts/NotoColorEmoji.ttf
var ColorEmojiFont []byte

//go:embed fonts/STIXTwoMath.otf
var MathFont []byte
