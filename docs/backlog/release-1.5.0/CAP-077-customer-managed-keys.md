# CAP-077 — Customer-managed encryption keys (CMEK)

| Epic | Priority | Points | Depends on | Status |
|---|---|---|---|---|
| E13 Private networking & security | P1 | 8 | CAP-053, CAP-009 | Not started |

## User story
As a **security engineer**, I want to register my KMS keys with Capella, rotate them and attach them to clusters from
cbctl, so that clusters are encrypted with keys my organization controls.

## Context & discovery
CMEK keys are registered at the organization level (AWS, GCP, Azure) or, for Azure only, at the project level.
CAP-053 already accepts `--cmek-id` on `cluster create`; this story manages the keys themselves.

| Operation | Endpoint | Success | Notes |
|---|---|---|---|
| Cloud accounts | `GET {org}/cloudAccounts` | 200 | `aws-capella-account`, `gcp-capella-project`, `azure-capella-subscription`: the principals to grant key access |
| Azure app ID | `GET {org}/cmekAzureApplication`, `GET …/projects/{p}/cmekAzureApplication` | 200 | `id` |
| Enable provider | `PUT {org}/cmek/providers`, `PUT …/projects/{p}/cmek/providers` | 204 | `cloudProvider` (`aws`, `gcp`, `azure`; project: `azure`) |
| Create key | `POST {org}/cmek`, `POST …/projects/{p}/cmek` | 200 / 201 | `name`, `description`, `config` (AWS `arn`; GCP `resourceName`; Azure `keyLocation`, `region`) → `id` |
| List / get | `GET {org}/cmek[/{cmekId}]`, `GET …/projects/{p}/cmek[/{cmekId}]` | 200 | |
| Rotate | `PUT {org}/cmek/{cmekId}`, `PUT …/projects/{p}/cmek/{cmekId}` | 204 | New `config` |
| History | `GET {org}/cmek/{cmekId}/history` | 200 | `config`, `active`, `associatedAt`, `associatedBy` |
| Delete | `DELETE {org}/cmek/{cmekId}`, `DELETE …/projects/{p}/cmek/{cmekId}` | 204 | |
| Associate / unassociate | `POST …/clusters/{c}/cmek/{cmekId}/associate` / `…/unassociate` | 204 | |

## Design

### Commands
```
cbctl capella cmek accounts
cbctl capella cmek azure-app [--project NAME]
cbctl capella cmek enable-provider aws|gcp|azure [--project NAME]
cbctl capella cmek create  NAME (--aws-arn ARN | --gcp-key RESOURCE | --azure-key URL --azure-region R) [--project NAME] [--description TEXT]
cbctl capella cmek list    [--project NAME]
cbctl capella cmek get     NAME [--project NAME]
cbctl capella cmek history NAME
cbctl capella cmek rotate  NAME (--aws-arn … | --gcp-key … | --azure-key … --azure-region …) [--project NAME]
cbctl capella cmek delete  NAME [--project NAME]
cbctl capella cmek associate   CLUSTER KEY
cbctl capella cmek unassociate CLUSTER KEY
```
Example:
```
cbctl capella cmek accounts
cbctl capella cmek enable-provider aws
cbctl capella cmek create prod-key --aws-arn arn:aws:kms:us-east-1:123456789012:key/abcd-1234
cbctl capella cmek associate nova-prod-01 prod-key
```

### capella.yaml
```yaml
cmekKeys:                              # organization level
  - name: prod-key
    aws: {arn: "arn:aws:kms:us-east-1:123456789012:key/abcd-1234"}
projects:
  - name: nova
    cmekKeys: [{name: nova-azure-key, azure: {keyLocation: "https://…", region: eastus}}]
    clusters:
      - name: nova-prod-01
        cmekKey: prod-key              # associated after create; replaces CAP-053's raw --cmek-id
```
- A changed key `config` is a **rotation** (`~ cmek key prod-key  rotate`), never a replace. Rotation asks for
  confirmation and shows the clusters using the key.
- `delete` and `unassociate` are refused (exit 4) while a cluster depends on the key, with the cluster names.
- `cmek` keys are never pruned by `apply --prune`; deletion is explicit (`cmek delete`).
- Code: `cli/cmek_cmds.py`, `resources/cmek_key.py`, 18 operations, fake Capella.

## Acceptance criteria & tests

### AC1 — Set up a key
**Given** `cmek enable-provider aws` and `cmek create prod-key --aws-arn …`, **then** one `PUT …/cmek/providers` and
one `POST {org}/cmek` with `config.arn`; capella.yaml records the key; `plan` reports no changes.

| Test | Location | Verifies |
|---|---|---|
| `test_cmek_ac1_create_aws` | `tests/scenario/test_cmek.py` | Also GCP and Azure bodies |
| `test_cmek_ac1_azure_project_level` | same | |

### AC2 — Idempotent
**Given** AC1 completed, **then** a re-run makes zero writes.

| Test | Location | Verifies |
|---|---|---|
| `test_cmek_ac2_rerun_zero_writes` | `tests/scenario/test_cmek.py` | |

### AC3 — Rotate
**Given** a new ARN, **then** `plan` shows a rotation listing the clusters using the key, and `apply` sends one `PUT`;
`history` lists both configs with `active` marked.

| Test | Location | Verifies |
|---|---|---|
| `test_cmek_ac3_rotate_and_history` | `tests/scenario/test_cmek.py` | |

### AC4 — Associate and guards
**Given** `cmekKey: prod-key` on a cluster, **then** `apply` associates it after the cluster is healthy. `cmek delete`
or `unassociate` of a key in use exits 4 naming the clusters; an unknown key name exits 3.

| Test | Location | Verifies |
|---|---|---|
| `test_cmek_ac4_associate` | `tests/scenario/test_cmek.py` | |
| `test_cmek_ac4_in_use_blocked` | same | |

### AC5 — Read commands
**Given** the read commands, **then** `accounts` prints the three principals with what to grant them, `azure-app`
prints the application ID, `list` prints `S.NO`, `NAME`, `PROVIDER`, `ID`.

| Test | Location | Verifies |
|---|---|---|
| `test_cmek_ac5_read_commands` | `tests/scenario/test_cmek.py` | |

### AC6 — sync and drift
**Given** keys in Capella, **then** `sync` writes them at the right level; `drift` lists unmanaged keys but `apply
--prune` does not delete them.

| Test | Location | Verifies |
|---|---|---|
| `test_cmek_ac6_sync_drift_never_pruned` | `tests/scenario/test_cmek.py` | |

## Out of scope
- Granting Capella access to the key in the customer's KMS.

## Open questions
- Can a cluster be associated with a key after creation, or only at create time (CAP-053 `--cmek-id`)? AC4 assumes
  after; if not, `cmekKey` on an existing cluster is BLOCKED as immutable.

<!-- ported-from: capellactl@6389d5a docs/release-1.5.0/CAP-077-customer-managed-keys.md · rules v1 -->
