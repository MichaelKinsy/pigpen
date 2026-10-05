# Milestones

- Confirmed both patch uploads succeeded with provenance; only the 12 × 5-second registry poll failed. Exact released artifacts were independently verified previously.
- Extended metadata/latest verification to 60 attempts, retaining integrity checks, monotonic latest protection and existing-version no-republish behavior. Failed-job reruns now use the ranking job's artifact ID rather than the current attempt's generated artifact name.
- Six tests execute the workflow's embedded publication script with mocked registry responses and no real delays or publication. Covered delayed metadata/latest beyond one minute, existing identical versions, mismatched integrity, backwards versions, bounded timeout and artifact output wiring. Added them to existing CI. No README or runtime/package changes.
