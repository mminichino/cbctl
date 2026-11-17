# Hibernation: power off/on and schedules

Turning a cluster off stops compute charges; storage, data, buckets, indexes, users and allowed IPs
remain. Linked App Services turn off too. Data API charges continue.

- On demand: `cbctl capella cluster power NAME --off` / `--on` (waits until done; free-tier clusters too).
- Weekly schedule: Capella turns the cluster off outside the on-windows.

```bash
cbctl capella cluster schedule set NAME --timezone US/Pacific --weekdays 08:00-18:00 --weekend off
cbctl capella cluster schedule set NAME --friday 08:00-14:30      # per-day override
cbctl capella cluster schedule show NAME                          # weekly grid and the next on/off time
cbctl capella cluster schedule delete NAME
```

`schedule set` records it in capella.yaml (`hibernation:`), so `apply` keeps it:

```yaml
hibernation:
  timezone: US/Pacific
  weekdays: "08:00-18:00"     # on-window Monday–Friday
  weekend: "off"
  friday: "08:00-14:30"       # per-day override
```

Rules from the API: times on the hour or half hour; every day cannot be off, and every day cannot
be on (to keep a cluster always on, delete its schedule). `24:00` ends a window at midnight
(`"08:00-24:00"`), and `"00:00-24:00"` is the whole day on. Quote `"on"`/`"off"`: unquoted YAML
reads them as true/false (cbctl capella converts them, with a tip).

- Pause for a demo or incident: `cbctl capella cluster schedule pause NAME --power-on`, later `resume`.
  Both record `paused` in capella.yaml.

While a cluster is turned off, changes to it and to what it holds are blocked (the schedule itself can
still change). Turn it on first, or pass `apply --power-on`.
