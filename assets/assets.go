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
