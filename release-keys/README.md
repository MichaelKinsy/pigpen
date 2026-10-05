# Release keys

The public half of the Ed25519 key that signs Piglet Binaries and release indexes (`pig piglet keygen` writes `<name>.key.pub`
beside the private key). Commit only `.pub` files here; `npm run quality` refuses private key material anywhere in the tree.

`scripts/generate-index.mjs` lists a Piglet as `available` only for a committed release receipt that one of these keys
verifies. Users pin the same key with `pig piglet trust add release-keys/<name>.pub`. Rotating the key means adding the new
`.pub` here (old receipts keep verifying against the old one) before the first release it signs. `pigpen-piglets.pub` is the key
`ed25519:fc4fbe0a1ba0f640a360bfd5a1c98874`; its private half is only the `release` environment's secret `PIGLET_SIGNING_KEY`.
