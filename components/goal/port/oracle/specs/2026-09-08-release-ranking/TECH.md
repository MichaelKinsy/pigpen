# Implementation

Use a standard-library Python script adapted from tmonk.github.io's catalog parser. Validate pagination, counts, duplicates, and descending download order before accepting a rank. Persist one observation per release and leave an existing release observation unchanged on retries. Render both SVGs and README alt text from the same observation; validate all replacements before writing.

Run a ranking preparation job before the publish job in publish.yml. On real releases, validate the stable tag, version, and main ancestry before updating main. Commit the observation and badges to main, then upload the observation file. The publish job downloads that file into the tagged checkout and renders its own README and badges before packing. Reuse an existing release observation and select only observations through that release when rendering, so later releases do not change a retry's package. Dry runs skip ranking changes. No daily schedule. Ordinary push only; a conflict fails before publishing.

Registry verification must tolerate delayed propagation of npm’s latest endpoint, using the same bounded retry period as version metadata verification.
