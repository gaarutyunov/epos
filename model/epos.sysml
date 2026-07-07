// =============================================================================
// Epos — consolidated SysML v2 domain model  (single file; see SPEC "Model-Driven
// Structure, BDD & Testing" section).
//
// Consumed by sysgo: this .sysml is converted to SysML v2 API JSON via
// scripts/sysml2json.sh (OMG Pilot Implementation serializer), then sysgo
// generates the DDD/hexagonal Go scaffold.
//
// sysgo DDD mapping (its §7) used throughout:
//   package                          -> bounded Context (top-level Go tree)
//   part def WITH identity attribute -> Aggregate root / Entity
//   part def WITHOUT identity        -> Value Object
//   attribute                        -> field (type-mapped, its §8)
//   part x : T [*]                   -> composition / child entity collection
//   port def                         -> Port (Go interface); driven -> app/port/out
//   in item -> method param;  out item -> method return
//   action def                       -> Use case (application interactor)
//   item def                         -> DTO
//   requirement def                  -> doc comment + test stub
// Driven ports live in app/port/out (sysgo default; ports.repository-in-domain=false).
// Only generic, domain-free clients live in the shared Infrastructure context.
// =============================================================================


// =============================================================================
// Shared Infrastructure context — ONLY generic, domain-free low-level clients
// reused by multiple contexts' adapters (OCI distribution, git, kubernetes).
// Domain-shaped ports stay in their owning context (hybrid decision).
// =============================================================================
package Infrastructure {

    attribute def HttpEndpoint {
        attribute url : String;
    }

    // Generic OCI distribution client (push/pull/tags/catalog blobs+manifests).
    // Wrapped by ORAS in the adapter. Domain-free.
    port def OciClient {
        in  item endpoint : HttpEndpoint;
        in  item repo     : String;
        in  item reference: String;   // tag or digest
        out item bytes    : String;   // opaque blob/manifest payload
    }

    // Generic git client (ls-remote, fetch subtree, resolve ref->commit, tree sha).
    port def GitClient {
        in  item remoteUrl : String;
        in  item ref       : String;
        in  item subpath   : String;
        out item commit    : String;  // resolved full commit SHA
        out item treeSha   : String;  // git tree object SHA of subpath@commit
    }

    // Generic kubernetes client (apply/get/delete ConfigMap & Secret objects).
    port def KubeClient {
        in  item namespace : String;
        in  item kind      : String;  // ConfigMap | Secret
        in  item name      : String;
        in  item manifest  : String;  // serialized object
        out item applied   : Boolean;
    }
}


// =============================================================================
// Packaging context — skill artifact, OCI media types, tarball, metadata,
// strict validation + dangling-reference lint.
// =============================================================================
package Packaging {

    // ---- Value objects -------------------------------------------------------

    attribute def MediaType     { attribute value : String; }

    attribute def SemVer {
        attribute raw   : String;
        attribute major : Integer;
        attribute minor : Integer;
        attribute patch : Integer;
        attribute build : String;   // '+' -> '_' on OCI tag push
    }

    attribute def Digest        { attribute algo : String; attribute value : String; }

    attribute def OciRef {
        attribute registry : String;
        attribute repo     : String;
        attribute tag      : String;
    }

    attribute def ContentLayer  { attribute mediaType : MediaType; attribute digest : Digest; attribute sizeBytes : Integer; }
    attribute def ConfigBlob    { attribute mediaType : MediaType; attribute digest : Digest; attribute sizeBytes : Integer; }

    attribute def SkillFile {
        attribute path      : String;
        attribute isBinary  : Boolean;
        attribute sizeBytes : Integer;
    }

    // ---- Entities / aggregate roots -----------------------------------------

    part def SkillMetadata {
        attribute name        : String;   // identity: lowercase/digits/hyphens,<=64,==dir
        attribute version     : SemVer;
        attribute description : String;   // required,<=1024,no XML
        attribute keywords    : String[*];
        attribute home        : String;
    }

    part def SkillArtifact {
        attribute id       : String;      // identity ⇒ aggregate root
        attribute metadata : SkillMetadata;
        attribute config   : ConfigBlob;
        attribute content  : ContentLayer;
        part files : SkillFile[*];
        port build : PackagingPort;
    }

    // ---- DTOs ----------------------------------------------------------------

    item def PackageRequest   { attribute sourceDir : String; }
    item def PackagedArtifact { attribute ref : OciRef; attribute digest : Digest; }
    item def ValidationReport { attribute ok : Boolean; attribute messages : String[*]; }

    // ---- Ports (driven -> app/port/out) -------------------------------------

    port def PackagingPort {
        in  item request  : PackageRequest;
        out item artifact : PackagedArtifact;
    }

    port def ValidationPort {
        in  item artifact : SkillArtifact;
        out item report   : ValidationReport;
    }

    // ---- Use cases -----------------------------------------------------------

    action def PackageSkill  { in request : PackageRequest;  out artifact : PackagedArtifact; }
    action def ValidateSkill { in artifact : SkillArtifact;  out report   : ValidationReport; }
    action def PushSkill     { in ref : OciRef; in artifact : SkillArtifact; out pushed : PackagedArtifact; }
}


// =============================================================================
// Composition context — the unified layer stack: skill layers + overlay layers,
// local or pulled (OCI/git); pin capture; precedence; granularity; values.
// (SPEC §9)
// =============================================================================
package Composition {

    // ---- Value objects -------------------------------------------------------

    attribute def LayerKind   { attribute value : String; }   // skill | overlay
    attribute def SourceKind  { attribute value : String; }   // local | oci | git

    // Pin: OCI digest OR git commit+treeSha (unified record; SPEC §9.7).
    attribute def Pin {
        attribute sourceType : SourceKind;
        attribute source     : String;
        attribute version    : String;    // tag or ref (informational for git)
        attribute digest     : String;    // OCI: manifest digest
        attribute commit     : String;    // git: full commit SHA
        attribute treeSha    : String;    // git: tree object SHA of subpath
        attribute subpath    : String;    // git only
    }

    // A single overlay operation (SPEC §9.4). Byte/line oriented, format-agnostic.
    attribute def Operation {
        attribute op          : String;   // add-file|delete-file|append-to-file|
                                           // replace-in-file|patch-file
        attribute target      : String;   // path in layers below
        attribute payloadPath : String;   // sibling file (alt to inline)
        attribute content     : String;   // inline payload (alt to path)
        attribute pattern     : String;   // replace-in-file
        attribute replacement : String;   // replace-in-file
        attribute required    : Boolean;  // strictness (default false, SPEC §9.5)
    }

    // ---- Entities / aggregate roots -----------------------------------------

    // One layer in the stack. Skill layers carry files; overlay layers carry ops.
    part def Layer {
        attribute id     : String;        // identity
        attribute name   : String;        // also the values-nesting key (SPEC §9.8)
        attribute kind   : LayerKind;
        attribute source : SourceKind;
        attribute pin    : Pin;           // present for pulled layers
        part operations : Operation[*];   // present for overlay layers
    }

    // The ordered stack, resolved into one merged skill. Aggregate root.
    part def LayerStack {
        attribute id : String;            // identity ⇒ aggregate root
        part layers : Layer[*];           // low -> high precedence (order matters)
        port source : LayerSource;        // driven: fetch/pin pulled layers
        port merge  : CompositionPort;    // driven: resolve stack -> merged skill
    }

    // ---- DTOs ----------------------------------------------------------------

    item def ComposeRequest {
        attribute stackId : String;
        attribute values  : String;       // nested-under-name + global (SPEC §9.8)
    }

    item def MergedSkill {
        attribute skillId    : String;    // resulting composite skill
        attribute provenance : String[*]; // per-file: which layer supplied it
    }

    item def PinRecord {
        attribute name    : String;
        attribute pin     : Pin;
    }

    // ---- Ports (driven -> app/port/out) -------------------------------------

    // Domain-shaped: resolve a pulled layer's bytes and capture its pin.
    // Its adapter uses Infrastructure.OciClient / Infrastructure.GitClient.
    port def LayerSource {
        in  item layer : Layer;
        out item pin   : PinRecord;
    }

    // Resolve the ordered stack into one merged skill (later-overrides-earlier;
    // whole-file for assets, operation-merge for SKILL.md; SPEC §9.5).
    port def CompositionPort {
        in  item request : ComposeRequest;
        out item merged  : MergedSkill;
    }

    // ---- Use cases -----------------------------------------------------------

    action def CaptureDependencyPin { in layer : Layer;          out pin : PinRecord; }
    action def ComposeStack         { in request : ComposeRequest; out merged : MergedSkill; }
    action def VerifyPin            { in expected : PinRecord;   out ok : Boolean; }  // mismatch = hard error
}



// =============================================================================
// Registry context — pass-through proxy (stores no secrets), read-only listing
// secrets (env-var refs only), discovery (catalog auto-detect + registration).
// (SPEC §6, §8)
// =============================================================================
package Registry {

    // ---- Value objects -------------------------------------------------------

    attribute def DiscoveryMode { attribute value : String; }  // catalog | registered

    // Read-only listing credential: env-var references ONLY (never stored values).
    attribute def ListingSecret {
        attribute usernameEnv : String;   // usernameEnv ref, not the value
        attribute tokenEnv    : String;   // tokenEnv ref, not the value
    }

    attribute def RegistryEntry {
        attribute name         : String;
        attribute url          : String;
        attribute discovery    : DiscoveryMode;   // forces mode when set
        attribute repositories : String[*];       // guaranteed floor
        attribute namespaces   : String[*];       // enumerated where supported
    }

    // ---- Entities / aggregate roots -----------------------------------------

    // The registration index (aggregate root): the set of known registries/repos,
    // seeded from registries.yaml, possibly extended at runtime.
    part def RegistrationIndex {
        attribute id : String;                    // identity ⇒ aggregate root
        part entries : RegistryEntry[*];
        port store   : RegistrationStore;         // driven: persist the index
        port catalog : CatalogProbe;              // driven: probe _catalog
    }

    // ---- DTOs ----------------------------------------------------------------

    item def ProxyRequest {
        attribute method   : String;              // GET|HEAD|PUT|POST
        attribute repo     : String;
        attribute reference: String;
    }

    item def ProxyResponse {
        attribute status : Integer;
        attribute body   : String;                // opaque; credentials pass through
    }

    item def CatalogResult {
        attribute mode : DiscoveryMode;           // detected mode
        attribute repos: String[*];
    }

    // ---- Ports ---------------------------------------------------------------

    // Driven: persist/read the registration index.
    port def RegistrationStore {
        in  item index : RegistrationIndex;
        out item ok    : Boolean;
    }

    // Driven: probe /v2/_catalog?n=1 to auto-detect mode. Adapter uses OciClient.
    port def CatalogProbe {
        in  item entry  : RegistryEntry;
        out item result : CatalogResult;
    }

    // Driving: the transparent proxy boundary (inbound requests to relay).
    port def ProxyPort {
        in  item request  : ProxyRequest;
        out item response : ProxyResponse;
    }

    // ---- Use cases -----------------------------------------------------------

    action def ProxyManifest   { in request : ProxyRequest;  out response : ProxyResponse; }
    action def DetectDiscovery { in entry : RegistryEntry;   out result : CatalogResult; }
    action def RegisterRegistry{ in entry : RegistryEntry;   out ok : Boolean; }
    action def ListSkills      { in secret : ListingSecret;  out result : CatalogResult; }
}


// =============================================================================
// Signing context — cosign/Sigstore verify-when-present (opt-in enforce),
// via OCI 1.1 subject/referrers. (SPEC §7)
// =============================================================================
package Signing {

    // ---- Value objects -------------------------------------------------------

    attribute def Signature {
        attribute referrerDigest : String;   // referrers/subject descriptor
        attribute payload        : String;
    }

    attribute def VerifyPolicy {
        attribute requireSignature : Boolean; // --require-signature enforces
    }

    // ---- Entities / aggregate roots -----------------------------------------

    part def VerificationSubject {
        attribute id      : String;           // identity ⇒ aggregate (the signed artifact)
        attribute digest  : String;           // subject manifest digest
        part signatures : Signature[*];
        port verify : SignaturePort;          // driven: cosign verify via referrers
    }

    // ---- DTOs ----------------------------------------------------------------

    item def VerifyRequest {
        attribute subjectDigest : String;
        attribute policy        : VerifyPolicy;
    }

    item def VerifyResult {
        attribute verified : Boolean;
        attribute present  : Boolean;         // whether any signature existed
        attribute messages : String[*];
    }

    // ---- Ports ---------------------------------------------------------------

    // Driven: fetch referrers + verify cosign signatures. Adapter uses OciClient.
    port def SignaturePort {
        in  item request : VerifyRequest;
        out item result  : VerifyResult;
    }

    // ---- Use cases -----------------------------------------------------------

    action def VerifySignature { in request : VerifyRequest; out result : VerifyResult; }
}


// =============================================================================
// Stats context — count MANIFEST GETs only (exclude HEAD/blobs; handle
// multi-arch); Prometheus (aggregate/per-skill) or ClickHouse sink. (SPEC §10)
// =============================================================================
package Stats {

    // ---- Value objects -------------------------------------------------------

    attribute def PullEvent {
        attribute repo      : String;
        attribute reference : String;
        attribute isManifestGet : Boolean;   // only these are counted
        attribute timestamp : String;
    }

    attribute def SinkKind { attribute value : String; }  // prometheus | clickhouse

    // ---- Entities / aggregate roots -----------------------------------------

    part def DownloadCounter {
        attribute id : String;                // identity ⇒ aggregate root
        attribute skill : String;
        attribute count : Integer;
        port sink : StatSink;                 // driven: emit/aggregate
    }

    // ---- DTOs ----------------------------------------------------------------

    item def CountRequest { attribute event : PullEvent; }
    item def CountSnapshot { attribute skill : String; attribute total : Integer; }

    // ---- Ports ---------------------------------------------------------------

    // Driven: record a counted pull / read aggregates. Adapter = Prometheus|ClickHouse.
    port def StatSink {
        in  item request  : CountRequest;
        out item snapshot : CountSnapshot;
    }

    // ---- Use cases -----------------------------------------------------------

    action def RecordPull    { in request : CountRequest; out snapshot : CountSnapshot; }
    action def ReadStatistics{ in skill : String;         out snapshot : CountSnapshot; }
}


// =============================================================================
// Install context — materialize a composed skill to files OR mountable
// ConfigMap(s); lockfile with digest pins + bounded N-revision self-contained
// bundle history; rollback restores the whole bundle. (SPEC §4, §5, §14)
// =============================================================================
package Install {

    // ---- Value objects -------------------------------------------------------

    attribute def Target { attribute value : String; }   // files | configmap

    attribute def LockPin {
        attribute ref    : String;
        attribute digest : String;   // digest authoritative; mismatch = hard error
        attribute tag    : String;   // informational
    }

    // A self-contained revision record (version + values + digest-pinned layers).
    attribute def Revision {
        attribute number  : Integer;
        attribute version : String;
        attribute values  : String;
        attribute pins    : LockPin[*];
        attribute blob    : String;   // cluster: Helm-style gzip+base64 self-contained
    }

    // ---- Entities / aggregate roots -----------------------------------------

    // A named install handle (Helm-release-style). Aggregate root.
    part def Release {
        attribute id      : String;          // identity ⇒ aggregate root
        attribute name    : String;          // handle: names ConfigMap(s)/keys revisions
        attribute target  : Target;
        attribute namespace : String;        // configmap target only
        part revisions : Revision[*];        // bounded history (low..N)
        port revisionStore : RevisionStore;  // driven: persist revision history
        port materializer  : MaterializePort;// driven: write files | ConfigMap
    }

    // The lockfile: pins + embedded bounded bundle history (aggregate, identity=path).
    part def Lockfile {
        attribute id   : String;             // identity (path)
        part pins : LockPin[*];
        part history : Revision[*];
    }

    // ---- DTOs ----------------------------------------------------------------

    item def InstallRequest {
        attribute releaseName : String;
        attribute skillId     : String;      // merged skill from Composition
        attribute target      : Target;
        attribute namespace   : String;
        attribute values      : String;
    }

    item def InstallResult {
        attribute releaseName : String;
        attribute revision    : Integer;
        attribute ok          : Boolean;
    }

    item def RollbackRequest {
        attribute releaseName : String;
        attribute toRevision  : Integer;     // restores WHOLE bundle
    }

    // ---- Ports ---------------------------------------------------------------

    // Driven: persist/read revision history. Adapters: local file, ConfigMap/Secret,
    // PostgreSQL (SPEC §11); ConfigMap adapter uses Infrastructure.KubeClient.
    port def RevisionStore {
        in  item release  : Release;
        out item ok       : Boolean;
    }

    // Driven: write the merged skill to files or mountable ConfigMap(s).
    // ConfigMap adapter uses Infrastructure.KubeClient.
    port def MaterializePort {
        in  item request : InstallRequest;
        out item result  : InstallResult;
    }

    // ---- Use cases -----------------------------------------------------------

    action def InstallSkill { in request : InstallRequest;  out result : InstallResult; }
    action def UpgradeSkill { in request : InstallRequest;  out result : InstallResult; }
    action def RollbackSkill{ in request : RollbackRequest; out result : InstallResult; }
    action def UninstallSkill { in releaseName : String;    out ok : Boolean; }
    action def ReadHistory  { in releaseName : String;      out result : InstallResult; }
}


// =============================================================================
// Frontend context — federated, K8s-deployable web listing of skills across
// registered registries; filtering/search over the discovery data. (SPEC §12)
// =============================================================================
package Frontend {

    // ---- Value objects -------------------------------------------------------

    attribute def SkillCard {
        attribute name        : String;
        attribute version     : String;
        attribute description : String;
        attribute registry    : String;
        attribute downloads   : Integer;   // from Stats
    }

    attribute def Filter {
        attribute keyword  : String;
        attribute registry : String;
    }

    // ---- Entities / aggregate roots -----------------------------------------

    part def Catalog {
        attribute id : String;             // identity ⇒ aggregate root
        part cards : SkillCard[*];
        port feed : CatalogFeed;           // driven: gather federated listing
    }

    // ---- DTOs ----------------------------------------------------------------

    item def ListingRequest { attribute filter : Filter; }
    item def Listing        { attribute cards : SkillCard[*]; }

    // ---- Ports ---------------------------------------------------------------

    // Driving: the web boundary (inbound listing requests).
    port def FrontendPort {
        in  item request : ListingRequest;
        out item listing : Listing;
    }

    // Driven: gather cards across registries (uses Registry discovery + Stats).
    port def CatalogFeed {
        in  item filter  : Filter;
        out item listing : Listing;
    }

    // ---- Use cases -----------------------------------------------------------

    action def ListCatalog  { in request : ListingRequest; out listing : Listing; }
    action def FilterCatalog{ in request : ListingRequest; out listing : Listing; }
}
