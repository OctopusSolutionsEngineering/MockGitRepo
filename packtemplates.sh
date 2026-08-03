#!/bin/bash
set -e

tar -cjf repotemplate.tar.bz2 repotemplate
if [[ -f repotemplate.tar.bz2 ]]; then
    echo "repotemplate.tar.bz2 created successfully."
    #rm -rf repotemplate
else
    echo "Failed to create repotemplate.tar.bz2."
    exit 1
fi