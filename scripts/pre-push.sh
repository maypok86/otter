#!/bin/bash

# Run CI tests
readonly ci_cmd="make ci"
eval "$ci_cmd"

readonly result=$?
if [ $result -ne 0 ]; then
    echo -e "CI tests Failed!\n CMD: $ci_cmd"
    exit 1
fi
