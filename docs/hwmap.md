# hwmap processor

<!-- cspell:ignore hwmap milli -->

The `hwmap` processor renames metrics and reshapes their points by rules in the collector config. It exists for sources that give raw numbers and texts, such as SNMP tables, and for which the target names (`hw.temperature`, `hw.status`, `hw.fan.speed`) are a mapping table, not code. A new mapping needs a config change and no rebuild.

It is in the scrape image (`otelcol-scrape`) only. It does nothing until a metrics pipeline lists it, and with no `rules` it passes every point through untouched. It never runs a command.

## What a rule does

A rule picks the points of one metric, then:

1. renames the metric to `target`;
2. multiplies the value by `scale`, or drops the point when it equals `drop_when_value`;
3. or, for a `status` rule, turns the value or an attribute into `hw.status` points;
4. sets `hw.type`, `hw.id` and `hw.name`, fixed attributes and resource attributes.

The type stays the same: a gauge leaves as a gauge, a sum as a sum with its temporality and monotonic flag. Both timestamps of every point are kept. The first rule of a metric that matches a point takes it, in config order.

## Config

```yaml
processors:
  hwmap:
    rules:
      - name: disk-temperature        # unique; named in errors
        match:
          metric: snmp.disk.temperature.milli
          attributes:                 # optional; key must be present
            disk.kind: ssd            # a non-empty value must also be equal
        target: hw.temperature
        unit: Cel
        scale: 0.001
        drop_when_value: 0
        labels:
          hw.type: physical_disk
          hw.id: "HDD{disk.slot}"
          hw.name: "{disk.descr}"
        index_names:
          attribute: entity.index
          names: {"1001": "Board A"}
        constants:
          hw.limit_type: high.critical
        resource:
          hw.vendor: Example
          hw.model: Model 1
```

| Key | Meaning |
|---|---|
| `name` | Unique name of the rule. |
| `match.metric` | Name of the source metric. |
| `match.attributes` | Attribute keys the point needs. An empty value means the key must exist; a value must also be equal. The keys are yours: nothing about them is fixed. |
| `target` | Name of the metric the points leave as. |
| `unit` | Unit of the target. One of `Cel`, `W`, `V`, `A`, `By`, `1`, `rpm`, `s`, `ms`, `Hz`. Empty keeps the unit of the source (`1` for a status rule). The list is closed: it holds the units of the hw.* metrics and of the mapping tables in use, and a unit such as `J` or `bit/s` is added when a mapping needs it. |
| `scale` | A finite factor above 0; the value is multiplied by it and leaves as a double. Without it an integer stays an integer. With a `scale` and no `unit` the metric keeps the unit of the source, so set `unit` whenever the factor changes it. |
| `drop_when_value` | A point equal to this number is dropped. No count is raised. Use it for an unset threshold of 0. |
| `labels` | Point attributes from templates. `{key}` takes the value of the source attribute `key`. A template whose attribute is missing leaves that label off. |
| `index_names` | `hw.id` is the value of `attribute`; `hw.name` is looked up in `names` and falls back to the index. For sensors that have no name over SNMP. |
| `constants` | Fixed attributes, set on every emitted point as literal text. |
| `resource` | Fixed attributes (`hw.vendor`, `hw.model`) set on the resource of the batch that holds an emitted point. |
| `status` | See below. |

Later steps win: source attributes, then the index name, then labels, then constants.

Factors in use: millidegrees Celsius to `Cel` 0.001, milliwatts to `W` 0.001, `KiBy` to `By` 1024, percent to a ratio 0.01, units of 0.1 W to `W` 0.1.

## Status rules

A status rule turns a state into one point per listed state: `hw.status` is 1 for the state the device is in and 0 for every other state of the list, each point with the attribute `hw.state`.

```yaml
status:
  source: value          # value (default) or attribute
  attribute: smart       # the attribute that holds the state when source is attribute
  states: [ok, degraded, failed]   # default
  map:
    "1": ok
    "2": degraded
    "5": drop            # no point for this value
  default_state: degraded   # for a text that is not in the map
  state_attribute: hw.state    # default
  raw_attribute: hw.status.raw # default
```

- A value of `drop` in `map` gives no point and no count (a sensor that is not present).
- With a `default_state`, an unlisted text takes that state and the original text goes in `hw.status.raw` on every point of the group.
- With no `default_state`, an unlisted text leaves the point unchanged.
- Each unlisted text is counted per point in `otelcol_hwmap_unmapped_states{rule}` and logged once per value. After 1024 different values it logs one line and counts only.
- A numeric value is written as an integer for the lookup, so a double of 2.0 matches the key `"2"`.

## Counters

| Counter | Counts |
|---|---|
| `otelcol_hwmap_unmapped_points{metric}` | Points no rule matched (every point of a metric that has no rule, and points of a metric whose rules all said no). They pass unchanged. |
| `otelcol_hwmap_unmapped_states{rule}` | Points whose state text is not in the `status.map` of the rule. |

## Checks at load

The config does not load on a duplicate rule name or match, a missing name, metric or target, an unknown unit, a scale that is 0, negative, NaN or infinite, an unbalanced `{` in a template, an empty attribute key, a status map that points to a state not in `states`, a `default_state` not in `states`, an `attribute` source with no attribute, or an `index_names` with no attribute. The error names the rule.

## Limits

- A rule maps one metric to one metric. A value computed from two metrics is not available: a usage as size minus free over two volume metrics is not supported in 0.3.18.
- The rules of one metric are tried in order for each point, so keep them to tens. A metric with 10,000 rules costs about 1.3 ms for each point no rule matches; 10,000 rules over different metrics load in tens of milliseconds and cost nothing per point.
- A point that matches a rule whose state text is not in the map, with no default state, passes unchanged and is counted in `otelcol_hwmap_unmapped_states`, not in `otelcol_hwmap_unmapped_points`, because a rule did match it.
- Histograms and summaries pass unchanged and are counted.
- Texts taken from a point (state text, rendered labels) are made valid UTF-8 and cut at 256 bytes.

## Sample

`test/configs/scrape-hwmap.yaml` holds rules for temperatures with thresholds, a state enum, sensors named by index, a text state with a default, and unit conversions. The unit tests of the processor load that file.
