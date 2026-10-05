# Release procedure

Merge the green PR at its verified head, prepare a metadata-only release PR, then tag the merged release commit v0.31.4. Use publish.yml for validation, packing, OIDC publication, registry integrity verification and GitHub release creation.

Stage a fresh ranking observation before tagging and compare the updater's README output byte-for-byte with the approved README. Commit the observation with release metadata so the publication workflow reuses it rather than refetching; the checked observation leaves README and badges unchanged. Do not alter the publishing workflow or README.
