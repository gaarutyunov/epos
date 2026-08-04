# go-house

One Go skill derived from five published ones, with the house standard's fixed
dependency mandates turned into install-time parameters.

```sh
epos build -t go-house:0.1.0 .

epos install go-house:0.1.0 -f values-library.yaml   # lean
epos install go-house:0.1.0 -f values-service.yaml   # everything on
```

The second install does not rebuild: the artifact is already in the local store
and the profile is only read at install time.

| stage | source | what happens to it |
|---|---|---|
| `idiomatic` | spf13's `go` | keeps the philosophy; drops the standard-library cookbook, the debugging essay and the layout section this standard answers itself |
| `pro` | `golang-pro` | keeps generics, interfaces, concurrency and testing; drops `project-structure.md` whole, the `var _ Interface` section, the static worker pool and two generic shapes |
| `cli` | spf13's `cobra-viper` | keeps every Cobra pattern; drops the Viper half and puts the house's koanf answer in its place |
| `containers` | `testcontainers-go` | one Go example out of a skill with dozens |
| *(final)* | `go-project-scaffold` | the base; its `## Non-negotiable` table becomes parameters |

`tests/integration/example_go_house_test.go` builds this recipe with the real
builder and installs it under both profiles, so the documentation cannot
describe a build that no longer runs. It carries the `integration` tag because
every source is fetched over the network:

```sh
go test -tags=integration -run TestExampleGoHouse ./tests/integration/...
```
