#!/usr/bin/env bash
# Records demo.sh (a simulation, see its header) with asciinema and renders
# it to an animated SVG with svg-term-cli.
#
# Requirements: `asciinema` (brew install asciinema) and `svg-term`
# (npm install -g svg-term-cli). No nova binary, instance or credentials needed.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

asciinema rec demo.cast \
  --command "./demo.sh" \
  --output-format asciicast-v2 \
  --window-size 100x30 \
  --idle-time-limit 2 \
  --overwrite \
  --headless

svg-term --in demo.cast --out demo.svg --window --no-cursor --width 100 --height 30
rm -f demo.cast

# Reskin svg-term's built-in terminal background to NOVA's own dark
# background (--wb-zero-gravity-background-paper-elevation-1, #141623 in
# @wandelbots/design-tokens). The prompt color comes from demo.sh itself
# (a truecolor escape), so it needs no post-processing here.
sed -i '' 's/#282d35/#141623/' demo.svg

echo "wrote asciinema/demo.svg"
