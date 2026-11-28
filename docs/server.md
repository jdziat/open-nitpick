# The nitpick server

`nitpick-server` hosts nitpick's hosted components behind one listener, one
sign-in and one web interface, the hub. The [notary](notary.md) is the first
component. A later one adds its own routes, storage and pages, and gets
authentication, sessions, roles and the interface shell from the server instead of
building them again.

The server owns what is shared: the listener and its limits, sign-in, sessions,
the mapping from a person to roles, CSRF protection, security headers, `/healthz`
and the UI shell. A component owns its routes, its storage and its pages.

Run with no configuration file, the server is the notary and nothing else: the same
routes and flags as the notary command it replaced, with no sign-in and no
UI.

```sh
nitpick-server -addr 127.0.0.1:8484 -database-url "$NITPICK_DATABASE_URL" \
  -token ci:authoritative:$CI_SECRET
```

## The database

The server keeps all of its state in a SQL database, Postgres or MySQL, and does not
start without one: give it `-database-url`, or set `NITPICK_DATABASE_URL`, to a
`postgres://` or `mysql://` URL. Nothing is written to disk. It creates and updates
its own tables at start-up, under a lock, so any number of servers may start together
against one database, and a server that has to wait for the lock waits up to two
minutes. Both engines are tested in CI against real servers, not stand-ins.

What is in it:

| Table | Holds | Used by |
|---|---|---|
| `notary_records`, `notary_history` | records, and what a clear or a repair took out | the [notary](notary.md) |
| `insights_reviews` | reviews | [insights](insights.md) |
| `server_state` | sessions, sign-in in progress, the SAML replay guard, the sign-in limits, the CSRF key | the server |

Because none of it is in a process, a person who signed in through one server is
signed in on every other, a sign-in started on one may finish on another, an
assertion replayed against a second server is refused, and the sign-in limits count
across all of them. A server that cannot reach the database fails closed: it refuses
the sign-in or the request and logs why, and does not let one through.

Each server holds a pool of up to 20 connections, so the database's connection
limit must be above 20 times the number of servers.

To move from the file-backed server this one replaced, copy its data directory in
once: [`nitpick-server import`](notary.md#moving-records-from-a-data-directory).
Sessions are not copied: everyone signs in again.

## Turning on the hub

The hub is off until you configure a way to sign in. Pass a configuration file with
`-config`, or set `NITPICK_SERVER_CONFIG` (which suits a container):

```yaml
components: [notary]            # the default

session:
  idle: 30m                     # the defaults
  absolute: 12h

auth:
  password:
    users_file: /etc/nitpick/users

roles:
  - {role: authoritative, provider: password, user: alice}
```

With no provider configured, `/ui/` and `/auth/` answer 404. The file is read
strictly: a key the server does not know is an error, because a typo in a security
setting that was ignored would be the setting off. Every provider is built at
start-up, so one that cannot work stops the process instead of failing for the
first person who tries it. Secrets are read from files named in the configuration,
never written in it.

The server must sit behind TLS. Its cookies are `Secure`, so a browser sends them
only over HTTPS (and to `localhost`).

### Building the UI

The UI is a React app under `ui/`, built with Vite, Tailwind CSS and shadcn/ui, and
embedded in the binary. `make ui` installs its dependencies from the lockfile,
builds it, and copies the result into the directory the server embeds, each file
under a name that carries a hash of its content. `make server` does that and then
builds the binary. The release image does the same.

A plain `go build` skips it. The server it produces starts and serves its
components, and the UI pages say that the UI was not built. Node 24 is needed only
to build; the binary has no Node dependency.

The server sends a strict Content-Security-Policy: scripts, styles, fonts and
images come from the server itself, and nothing is inline. The build checks its
own output against that and fails on a `data:` URL or a file from another origin.

## Signing in

Three providers sit behind one interface, so the server does not hard-code a
vendor. Configure any of them together. None of them grants a role: that is the
role rules' job, below.

### Password

```yaml
auth:
  password:
    users_file: /etc/nitpick/users
```

The server creates no accounts and stores no credentials. You supply a users file,
one account per line, `name:hash`, optionally followed by `:group,group`:

```
alice:$argon2id$v=19$m=65536,t=3,p=4$…$…:ops
```

Make a hash with the server, from standard input so the password is not in the
process list:

```sh
printf %s "$PASSWORD" | nitpick-server -hash-password
```

Only argon2id is accepted. A line holding a plain password, or any other scheme, is
refused at start-up, and the error names the line and not its text. Account names
are not case-sensitive. The server warns if the file is readable by every account
on the machine; it holds hashes, but there is no reason to share it.

Sign-in is limited. Five failed attempts in 15 minutes lock the account for 15
minutes, and twenty from one address lock that address, so one account guessed at
from many addresses and many accounts guessed at from one are both stopped. A locked
account is refused before the password is hashed, and a correct password does not
lift a lock. Every refusal reads the same, whether the account does not exist, the
password is wrong or the account is locked, and an account that does not exist costs
the same time to refuse as one that does. The address is the connection's, unless
`auth.trust_forwarded_for: true`, which reads the last `X-Forwarded-For` entry and
is for a deployment behind a proxy that sets it. The limits are counted in the database, so they hold across
servers and a restart.

### OpenID Connect

```yaml
auth:
  oidc:
    issuer: https://accounts.example.com
    client_id: nitpick
    client_secret_file: /etc/nitpick/oidc-secret
    redirect_url: https://nitpick.example.com/auth/oidc/callback
    groups_claim: groups          # the default
    label: Example SSO
```

This is the authorization code flow with PKCE. The provider is discovered at
start-up. The `state`, the nonce and the PKCE verifier are kept on the server, and a
sign-in is tied to the browser that began it by a cookie, so a sign-in someone else
started cannot be finished in your browser. A `state` is good for one answer. The ID
token must be signed by the provider's published key, name the issuer and this
client as audience, be unexpired and carry the nonce. A person's identity is the
issuer and the `sub`, so the same `sub` from two providers is two people. What the
person is told on a failure does not carry the reason, which goes to the log.

### SAML 2.0

```yaml
auth:
  saml:
    idp_metadata_file: /etc/nitpick/idp.xml
    entity_id: https://nitpick.example.com/auth/saml/metadata
    acs_url: https://nitpick.example.com/auth/saml/acs
    key_file: /etc/nitpick/sp.key
    cert_file: /etc/nitpick/sp.crt
    groups_attribute: groups      # the default
```

The provider's metadata comes from a file you give, so the certificate its
assertions are signed with is the one you chose and not one the network supplied.
Give the provider the server's metadata from `/auth/saml/metadata`. Requests go by
the redirect binding and answers come back by POST to `/auth/saml/acs`.

A response is accepted only if all of this holds, and each refusal has its own test:

- it answers a request this server made. Responses the provider started on its own
  are refused;
- an XML signature by the certificate in the metadata covers the assertion, or the
  response that holds it, and the element the server reads is the element the
  signature covers. An unsigned response, a response changed after signing, and a
  signature wrapped around a forged assertion (four arrangements are tried) are all
  refused;
- the audience is this server, the destination and recipient are the ACS URL, the
  issuer is the provider, the status is success and the assertion is within its
  validity window;
- the assertion's ID has not signed anyone in before. A genuine assertion is good
  once, and the server remembers the IDs it has accepted until they would have
  expired anyway;
- the answer arrives in the browser that began the sign-in. That browser's cookie
  is `SameSite=None`, because the answer is a cross-site POST that a `Lax` cookie
  would not accompany, and it holds only a random value.

## Roles

A sign-in grants nothing by itself. Roles come from rules, and a person holds the
union of every rule that matches them, or none:

```yaml
roles:
  - {role: authoritative, provider: oidc, group: nitpick-admins}
  - {role: read, provider: password, user: alice}
```

A rule names its `provider` and selects by exactly one of `user` (the provider's
stable name for them) and `group` (a group the provider reported). The provider is
required: a rule without one would match the same user or group on every sign-in
provider. A rule with no provider, with neither or both selectors, or naming a
provider that is not configured, is refused at start-up. So is a role that no enabled component declares: a typo such as
`authorative` would otherwise be the permission quietly not given, and the error
lists the roles that exist. The notary's are `authoritative`, `provisional` and
`read`. A person's roles are fixed when they sign in and last as long as the
session.

## Sessions and request protection

A session is a random 256-bit identifier in a cookie named `__Host-nitpick_session`:
`HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/` and no `Domain`, which the `__Host-`
prefix makes a browser enforce. The server keeps sessions in the database, keyed by a hash
of the identifier, so every server accepts a session any server issued, a restart
signs no one out, and a dump of the table holds nothing that works as a cookie. A session ends at the first of: it has been idle for
`session.idle`, it is `session.absolute` old, or the person signs out. Signing in
always issues a new identifier.

A request that changes state and is authorised by a cookie needs a CSRF token, and
is refused if the browser says it came from another site. The token is an HMAC of a
secret bound to the session, so it is no use to anyone who cannot already read the
session; the login form has one of its own, bound to a cookie. A request carrying a
bearer token is not asked for one, since another site cannot attach it. The one
exception is the SAML consumer, which the identity provider posts and which is bound
to the sign-in it answers instead.

Every response carries `X-Content-Type-Options: nosniff`, `Referrer-Policy:
no-referrer`, `Cross-Origin-Opener-Policy: same-origin` and a
`Content-Security-Policy` that allows only the server's own script and style,
forbids framing and a `<base>` tag, and has no `unsafe-inline` or `unsafe-eval`.
Pages are not cached. A path that contains `//`, `.` or `..` segments is refused with
404 before routing, so a client cannot be redirected onto a route and have a write
performed there.

The text on a page comes from code the trust model treats as hostile (diffs, model
output). Component pages are `html/template`, which the shell parses, so a component
cannot switch the escaping off, and an error's text goes to the log and not to the
page.

## The notary pages

With an `authoritative` role, the hub shows what the notary holds. Only that role
may list or open records, on the pages and on `GET /v1/records`; a provisional or
read token reads and writes single records by hash, as before, and is refused the
listing (403).

`GET /v1/records` takes `model` and `version` (exact), `since` and `until` (RFC 3339),
`limit` (default 50, most 200) and `cursor`, and returns `{"records": […],
"next_cursor": "…"}` newest first. An empty store answers 200 with `"records": []`; a
store that cannot be read answers 500, so an empty list is never a failed read.

The record page shows the model, tool version, who wrote the record, the reasoning
level, how many times it has been cleared, whether it is complete (a review with
these inputs replays it) or incomplete (it resumes, asking only for the answers it
lacks), and each answer with the request it is for and its slot, the unusable ones
marked. It shows what a record holds. A record holds the answers and not the prompts
or the stage that asked, so the page does not show a stage; keeping them would
change the record format, which is a decision of its own. The pages only read: a
page request never writes or deletes a record.

## The insights pages

With `insights` in `components`, the hub also shows the findings that reviews report:
a dashboard, the files that keep producing findings, and an MCP endpoint for
agents. See [Insights](insights.md).

## Deploying

The image and chart are published to `ghcr.io` as `nitpick-server` when a release tag
is pushed. For Kubernetes, put the configuration and the files it names in a Secret
and name it:

```sh
kubectl create secret generic nitpick-server \
  --from-file=server.yaml --from-file=users
helm install nitpick oci://ghcr.io/nitpick-oss/charts/nitpick-server \
  --set 'tokens[0].name=ci' --set 'tokens[0].role=authoritative' --set 'tokens[0].hash=<64 hex>' \
  --set config.existingSecret=nitpick-server
```

The Secret is mounted read-only under `/etc/nitpick/`, so the configuration refers to
`/etc/nitpick/users`, `/etc/nitpick/oidc-secret` and so on. With Docker Compose,
uncomment the two lines under `volumes` in `deploy/docker-compose.yml` and set
`NITPICK_SERVER_CONFIG`.

The chart starts a database of its own unless you point it at one, and runs as many
servers as `replicaCount` says: see [the notary's Kubernetes
section](notary.md#kubernetes). With Docker Compose, `deploy/docker-compose.yml`
starts a Postgres beside the server. For MySQL, swap the `database` service for:

```yaml
  database:
    image: mysql:8.4
    environment:
      MYSQL_DATABASE: nitpick
      MYSQL_USER: nitpick
      MYSQL_PASSWORD: ${NITPICK_DB_PASSWORD:?set it in .env}
      MYSQL_ROOT_PASSWORD: ${NITPICK_DB_PASSWORD}
    volumes: [database:/var/lib/mysql]
    healthcheck:
      test: ["CMD-SHELL", "mysqladmin ping -h 127.0.0.1 -unitpick -p$$MYSQL_PASSWORD"]
      interval: 5s
      retries: 20
```

and set the server's `NITPICK_DATABASE_URL` to
`mysql://nitpick:${NITPICK_DB_PASSWORD}@database:3306/nitpick`.

## Writing a component

A component implements `server.Component`: a name, the URL prefixes it owns, its
routes, its pages and a health check. A route names the role it needs, and the server
refuses a request without a session (401) or without that role (403) before the
handler runs, or leaves the role empty to authenticate the request itself, as the
notary does with its machine tokens. Routes outside the component's prefixes and two
components on one prefix are errors at start-up. A component that declares its roles by implementing
`server.RoleDeclarer` gets a role rule that names any other role refused, with the
real roles listed. A disabled component mounts nothing, so its routes are 404.
