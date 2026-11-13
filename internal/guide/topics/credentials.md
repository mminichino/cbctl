# Database credentials

Basic credentials grant `read`/`write` (`data_reader`/`data_writer`) on whole buckets, scopes or
collections. Omitting `resources` grants access to ALL buckets (validate warns).

```yaml
databaseCredentials:
  - name: orders_app
    password: ${ORDERS_APP_PASSWORD}
    access:
      - privileges: [data_reader, data_writer]
        resources:
          buckets:
            - name: orders
              scopes: [{ name: inventory, collections: [items] }]
```

- Passwords come from `${VAR}` or `${file:...}`. Omit the password to let Capella generate one; it
  is saved once to `./.cbctl/credentials/<cluster>.<name>.json` (mode 0600) and never printed.
  Add `.cbctl/` to `.gitignore`.
- Capella never returns passwords, so they are not compared and never changed by `apply`.
  Rotate explicitly: `cbctl capella credential update CLUSTER NAME --rotate-password`, or every planned
  credential with `cbctl capella apply --rotate-passwords [--target credential/CLUSTER/NAME]`. The configured
  value is used; without one a new password is generated and saved (the previous file is kept as `.bak`).
- Passwords must have at least 8 characters with an upper-case letter, a lower-case letter, a digit and a
  special character, and must not contain any of `< > ; . * & | £`; `validate` checks configured values offline
  (never printing them).

Commands:

```
cbctl capella credential list CLUSTER
cbctl capella credential get CLUSTER NAME
cbctl capella credential create CLUSTER NAME --privileges read+write --bucket orders [--scope S [--collection C]]
cbctl capella credential update CLUSTER NAME [--privileges … --bucket …] [--rotate-password]
cbctl capella credential delete CLUSTER NAME        (the local password file is kept)
```

Removing a credential from the file deletes nothing; `cbctl capella apply --prune` does. Advanced credentials
(`userRoles`) and custom database roles are not managed: one with a declared name is BLOCKED, and
`--prune` leaves undeclared ones alone.
