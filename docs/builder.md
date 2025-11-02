# Builder Service Notes

## Callback suppression

The builder suppresses callback and log traffic after a 4xx response from the API to avoid hot loops. Suppression lasts for the duration configured by `CALLBACK_SUPPRESSION_TTL_SECONDS` (default 600 seconds). A new deployment clears the suppression cache for its project.

For local development the compose stack sets a shorter TTL of 300 seconds so retries resume quickly. Adjust the value per environment by exporting the variable or editing `docker-compose.yml` under the `builder` service.
