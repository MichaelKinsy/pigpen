# Release ranking updates

Refresh pi-goal-x's ranking automatically before publishing a stable release, never on a daily schedule or publication dry run. Follow the website's method: rank by downloads among Pi extensions and display the best recorded rank, using the latest observation to break ties. Update both badge themes and README accessibility text together. Keep release observations for future comparisons.

The ranking update must finish before publication, and the npm package must include the updated badges and README. Save the observation before publishing so retries use the same data.

Publish these changes as patch release 0.31.2, including the simplified README and package description. Commit and push the changes, then verify GitHub and npm publication.
