#!/bin/bash
set -e

# Pack the loose objects in every template repository before archiving.
#
# A loose object is one small file on disk, and each template repo keeps them in
# hundreds of two character fan out directories. Copying the template to a
# temporary directory costs a round trip per file and per directory, which is
# slow on a network filesystem such as an Azure Files mount, so collapsing the
# loose objects into a packfile makes every copy at runtime much faster.
while IFS= read -r -d '' gitdir; do
    repo="$(dirname "$gitdir")"
    before=$(find "$gitdir/objects" -type f -path '*/??/*' 2>/dev/null | wc -l | tr -d ' ')
    git -C "$repo" gc --prune=now --quiet
    after=$(find "$gitdir/objects" -type f -path '*/??/*' 2>/dev/null | wc -l | tr -d ' ')
    echo "Packed $repo: $before loose objects -> $after"
done < <(find repotemplate -type d -name .git -print0)

tar -cjf repotemplate.tar.bz2 repotemplate
if [[ -f repotemplate.tar.bz2 ]]; then
    echo "repotemplate.tar.bz2 created successfully."
    #rm -rf repotemplate
else
    echo "Failed to create repotemplate.tar.bz2."
    exit 1
fi
