#!/usr/bin/env bash

set -euo pipefail

INPUT="${INPUT_COVERAGE-}"

# Create an HTML report.
if [[ "${INPUT_REPORT-true}" == "true" ]]; then
	go tool cover -html="$INPUT" -o coverage.html
fi

# Extract total coverage: the decimal number from the last line of the function report.
COVERAGE=$(go tool cover -func="$INPUT" | tail -1 | grep -Eo '[0-9]+\.[0-9]')

echo "coverage: $COVERAGE% of statements"

# Pick a color for the badge.
if awk "BEGIN {exit !($COVERAGE >= 90)}"; then
	COLOR=brightgreen
elif awk "BEGIN {exit !($COVERAGE >= 80)}"; then
	COLOR=green
elif awk "BEGIN {exit !($COVERAGE >= 70)}"; then
	COLOR=yellowgreen
elif awk "BEGIN {exit !($COVERAGE >= 60)}"; then
	COLOR=yellow
elif awk "BEGIN {exit !($COVERAGE >= 50)}"; then
	COLOR=orange
else
	COLOR=red
fi

# Style for the badge.
STYLE="${INPUT_BADGE_STYLE-flat}"
# Title for the badge.
TITLE="${INPUT_BADGE_TITLE-coverage}"

# Download the badge.
curl -s "https://img.shields.io/badge/$(printf %s "$TITLE" | jq -sRr @uri)-$COVERAGE%25-$COLOR?style=$STYLE" > "coverage.svg"
