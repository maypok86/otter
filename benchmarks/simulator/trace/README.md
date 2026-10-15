# Traces

| Directory | Traces | Format | Source |
|---|---|---|---|
| `arc` | `ds1`, `oltp`, `p3`, `p8`, `s3` | ARC | the authors of the ARC algorithm |
| `lirs` | `2_pools`, `backf`, `cpp`, `cs`, `gli`, `loop`, `multi1`, `multi2`, `multi3`, `ps`, `scan`, `sprite`, `zigzag` | LIRS | the authors of the LIRS algorithm |
| `address` | `gcc`, `gzip`, `mcf`, `swim`, `twolf` | LIRS | UCSD program address traces |
| `cache2k` | `orm-busy`, `orm-night`, `web07`, `web12` | LIRS | headissue GmbH, Jens Wilke; CC BY 4.0, see `cache2k/LICENSE.txt` |
| `cloud_physics` | `w106` | LIRS | CloudPhysics block I/O traces |
| `corda` | `trace_vaultservice` | Corda | Corda's vault service |
| `scarab` | `scarab-recs` | Scarab | Scarab Research |

The `lirs`, `address`, `cache2k`, `cloud_physics`, `corda` and `scarab` traces are the ones
bundled with Caffeine's simulator (https://github.com/ben-manes/caffeine, `simulator/src/main/resources`).
The `address`, `cache2k` and `cloud_physics` traces were rewritten to LIRS (one key per line) with
its `Rewriter`, since otter's simulator has no parser for their original formats; the keys and
their order are unchanged. Each trace has a config of the same name in `configs/`.
