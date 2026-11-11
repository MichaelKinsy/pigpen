The REA payload for one target is generated here by `node components/rea/scripts/payload.mjs [<os>/<arch>]` and embedded
into the `rea` member when PiG builds the Binary. The generated `<os>_<arch>.tar.zst` and `<os>_<arch>.json` are not
committed. A Binary built without them starts, reports that it carries no REA payload, and registers no REA server.
