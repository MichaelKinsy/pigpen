# Publication verification recovery

npm accepts releases before registry metadata is available. Give metadata and latest-tag verification up to five minutes each instead of one minute, retaining exact tarball integrity checks and no-republish behavior for existing versions. Preserve the ranking artifact dependency when rerunning only a failed publish job. Verify v0.31.8 through the repaired workflow without modifying its tag, package or existing release. Do not change the README or add a package version for workflow maintenance.
