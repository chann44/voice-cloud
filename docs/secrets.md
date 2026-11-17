# Provider API keys

Administrators can enter a provider's API key in **Providers → Create/Edit →
Provider API key**. The API encrypts it before writing SQLite, then decrypts it
only on the server to authenticate model discovery and inference. Clients keep
using their separate `vc_...` router keys. Saving a key does not implement a
provider's native protocol; a supported adapter is still required.

## Configure storage once

The self-hosted encrypted credential store uses AES-256-GCM with a fresh random
nonce for each save. Set `SECRETS_MASTER_KEY` to a base64-encoded, randomly
created 32-byte key in the **API service** environment. The master key is never
stored in SQLite or sent to the web service. Do not derive it from a password,
reuse an API key, put it in frontend variables, or commit it.

For local development, create a private file once, outside the database directory:

```sh
mkdir -p .secrets
chmod 700 .secrets
(umask 077; set -C; openssl rand -base64 32 > .secrets/provider-master-key)
export SECRETS_MASTER_KEY="$(cat .secrets/provider-master-key)"
make db-up
```

Do not rerun the generation command over an existing key. `.secrets/` is ignored
by Git. Keep a separate protected recovery copy. On subsequent starts, export the
same file's value. Compose forwards this variable only to the backend; its worker
process has it removed. Start/recreate the backend after setting it:

```sh
docker compose -f docker/compose.yml up --build -d
```

For a hosted installation, inject this value from your deployment's secret
manager, protect admin sessions, serve the console over HTTPS, and use HTTPS for
remote provider APIs. The application-managed store is encrypted SQLite; it is
not an integration with a hosted vault or KMS, and it does not protect against a
compromised API process or host administrator.

If no master key is configured and no keys have been stored, environment-based
credentials and unauthenticated local providers continue working. Saving a key
then returns a configuration error without creating or partially editing the
connection. Invalid master-key configuration fails API startup. When stored keys
exist, missing/wrong master keys or tampered ciphertext also fail startup.

## Save, replace, and remove

- **Save:** enter the actual provider key in the password field. The environment
  credential reference is cleared so the source of authentication is unambiguous.
- **Keep:** leave the key field blank when editing. The current encrypted key stays.
- **Replace:** enter a new key. The previous ciphertext is replaced atomically
  with the connection edit. This does not revoke the previous key at the provider.
- **Remove:** select **Remove saved API key and environment credential reference**.
  Both authentication sources are cleared. Revoke the original key at the provider
  if you want it invalidated outside Voice Cloud too.

Stored keys are never returned by list, detail, or mutation APIs. The dashboard
shows only whether a key is saved and whether server credentials are available.
It clears the password input after a save attempt. Members cannot create, replace,
or remove keys. There is no endpoint for reading decrypted keys.

Each encrypted value is bound to its connection ID and base URL. Changing the URL
of a connection with a saved key requires replacing or removing that key; renaming
it does not. Corrupt/mismatched ciphertext never falls back to an environment key.
Redirects are not followed when attaching credentials to provider requests.

`credential_ref` remains an advanced alternative naming an API-process environment
variable. It is used only when there is no stored key. The storage master key
cannot be used as a provider or decision-service credential. Decision models and text-chat routing are not supported.

## Backup and recovery

Migration 6 adds `provider_secrets` with ciphertext only. Existing connections and
environment references remain unchanged. Database backups include ciphertext,
but **the matching master key must be backed up separately**. Restore both to
recover saved provider keys. Losing the master key makes those stored keys
unrecoverable; simply generating a new one cannot decrypt them.

Changing the master key does not rotate existing ciphertext. Automated master-key
rotation and external KMS integration are not implemented. Keep the original key
for the lifetime of the encrypted data and its retained backups.

Deleting a provider connection cascades to its active encrypted key. Removing or
replacing a key does not erase old copies from SQLite free pages/WAL or retained
backups; those copies remain encrypted. Apply your normal retention policy and
revoke exposed or retired credentials at the upstream provider.
