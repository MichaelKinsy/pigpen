basket-v0.7.0.jsonl         A session recorded with the PiG v0.7.0 linux/amd64 release binary
                            (pig version 0.7.0+1.5.0) against the hermetic mock model, prompt
                            "run the [basket] scenario", built-in tools only.
basket-v0.7.0-parked.jsonl  The first 6 lines of the file above: header, model_change,
                            thinking_level_change, system, user, assistant (toolCall, stopReason
                            toolUse). This is the exact on-disk shape a parked approval leaves
                            (state-options.md Q1 t2/t13: the process dies after the assistant
                            entry and before any toolResult).
