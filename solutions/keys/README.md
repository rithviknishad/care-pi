# Signing keys of index.json and the solution locks

This directory holds the minisign **public** keys that the agent trusts (`*.pub`). The agent embeds them at build
time (`solutions/embed.go`). The private key is never in the repository: it is the repository secret `MINISIGN_KEY`.

`care-pi.pub` is the key of the releases of this repository. Until the owner makes it (the key ceremony of
`docs/solutions.md` section 18), this directory has no key, CI publishes no signatures, and
`updates.require_signed: true` fails with `lock-signature-invalid` ("no trusted key").

A host can also trust keys of its own: `/etc/care-pi/keys/*.pub` (for example the key of a self-hosted index,
`updates.index_url`).
