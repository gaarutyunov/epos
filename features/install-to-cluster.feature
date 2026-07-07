Feature: Install a skill to a Kubernetes cluster
  As a platform operator
  I want to install a skill as mountable ConfigMap(s)
  So that agents in the cluster can consume the skill from a mounted volume

  Background:
    Given a running OCI registry
    And a running Kubernetes cluster
    And a published skill "pdf-tools" version "1.4.2"

  Scenario: Template emits ConfigMap YAML without credentials
    When I run "epos template pdf pdf-tools --target=configmap -n skills"
    Then valid ConfigMap YAML is emitted
    And the YAML contains no registry credentials
    And file paths are reconstructed via items[].path

  Scenario: Install writes ConfigMap(s) into the namespace
    When I run "epos install pdf pdf-tools --target=configmap -n skills"
    Then a ConfigMap named for the release exists in namespace "skills"
    And the skill files can be mounted as a projected tree

  Scenario: A large skill auto-splits past the size ceiling
    Given a skill whose files exceed the 1 MiB ConfigMap ceiling
    When I run "epos install big big-skill --target=configmap -n skills"
    Then multiple ConfigMaps are created, one per subtree
    And each ConfigMap name is suffixed from the release handle

  Scenario: Cluster rollback uses in-cluster revision records
    Given release "pdf" installed to the cluster at revision 2
    When I run "epos rollback pdf 1 --target=configmap -n skills"
    Then the ConfigMap content matches revision 1
    And the rollback works without any local lockfile
