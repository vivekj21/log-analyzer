# Test data

| Folder | What it is |
|---|---|
| `logs/` | Small sample logs written for this project. |
| `production_logs/prod-k8s-*.log` | Larger logs generated for this project by `scripts/generate_massive_prod_logs.py`. |
| `production_logs/real-*.log` | **Third-party**, from [Loghub](https://github.com/logpai/loghub). Not stored in the repository, see below. |

## The `real-*.log` files (Loghub)

Six real-world sample logs (HDFS, Linux, OpenSSH, OpenStack, Spark, ZooKeeper) are used by `TestRealProductionLogs` in `internal/agent`. They come from Loghub, whose terms allow use for research or academic work and ask that you refer to the Loghub repository and cite its paper. Log Analyzer is Apache-2.0, so these files are **not** kept in this repository. The full notice is in [`production_logs/LOGHUB-NOTICE.txt`](production_logs/LOGHUB-NOTICE.txt).

To get them:

```bash
scripts/fetch-loghub-samples.sh     # `make test` and `make ci` do this for you
```

The script downloads each file from a fixed Loghub commit, converts it to Windows line endings (on purpose: the tests check that case), and checks a SHA-256. The files are ignored by Git. If they are missing, the test is skipped with a message, unless `LOG_ANALYZER_REQUIRE_SAMPLES=1` is set (`make test` sets it).

They contain real third-party IP addresses and user names from the original systems. Do not publish them elsewhere without checking the Loghub terms.
