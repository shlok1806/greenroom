import Foundation

// Which builds are running and whether they are behind main (root ADR 0033). Pure values and
// rules, each with a test (`BuildsTests`): the app's own stamp, the daemon's `/api/version`,
// what `scripts/update.sh --check` says, an update's output, and how the Greenroom section
// reads all of it. `Updates` owns the state; `GreenroomPanel` only draws it.

// MARK: - Stamps

/// A build's identity as its install script stamped it: the commit (short sha), whether the
/// tree had local changes, and when it was built. An unstamped build (`swift run`, `go run`)
/// has no commit.
struct BuildStamp: Equatable, Sendable {
    var commit: String
    var dirty = false
    var builtAt: Date?

    static let unknown = BuildStamp(commit: "")

    var known: Bool { !commit.isEmpty }

    /// Whether this build is `sha`. Short shas differ in length between repositories, so one
    /// being a prefix of the other is a match.
    func isCommit(_ sha: String) -> Bool {
        let a = commit.lowercased(), b = sha.lowercased()
        guard !a.isEmpty, !b.isEmpty else { return false }
        return a.hasPrefix(b) || b.hasPrefix(a)
    }

    /// Both built from the same commit (local changes aside).
    func sameCommit(as other: BuildStamp) -> Bool { known && other.known && isCommit(other.commit) }

    /// "abc1234", "abc1234 + local changes", "not stamped".
    var label: String {
        guard known else { return "not stamped" }
        return dirty ? "\(commit) + local changes" : commit
    }
}

/// The Companion's own build, from the keys `scripts/bundle.sh` writes into Info.plist.
struct AppBuild: Equatable, Sendable {
    var stamp: BuildStamp
    /// The checkout it was bundled from.
    var checkout: String?

    static let infoKeys = (commit: "GreenroomCommit", dirty: "GreenroomDirty", builtAt: "GreenroomBuiltAt", checkout: "GreenroomCheckout")

    static func from(info: [String: Any]?) -> AppBuild {
        let info = info ?? [:]
        let commit = (info[infoKeys.commit] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        let dirty = (info[infoKeys.dirty] as? Bool) ?? ((info[infoKeys.dirty] as? String) == "true")
        let builtAt = (info[infoKeys.builtAt] as? String).flatMap(DaemonDate.parse)
        let checkout = (info[infoKeys.checkout] as? String).flatMap { $0.isEmpty ? nil : $0 }
        return AppBuild(stamp: BuildStamp(commit: commit, dirty: dirty, builtAt: builtAt), checkout: checkout)
    }

    /// This process: a bundle `scripts/bundle.sh` made, or an unstamped `swift run`.
    static var current: AppBuild { from(info: Bundle.main.infoDictionary) }
}

/// `GET /api/version` (`apps/daemon/internal/api/version.go`). Every field is optional so a
/// partial answer still decodes; a daemon from before the route answers 404.
struct DaemonVersion: Decodable, Equatable, Sendable {
    var version: String?
    var commit: String?
    var dirty: Bool?
    var builtAt: String?
    var inputHelper: Int?
    var imageRecipe: Int?
    /// "nim", "manual" or "none".
    var verifier: String?
    var verifierModel: String?
    var visionModel: String?
    /// The checkout the daemon was installed from; empty when it was started another way.
    var checkout: String?

    var stamp: BuildStamp {
        BuildStamp(commit: commit ?? "", dirty: dirty ?? false, builtAt: builtAt.flatMap(DaemonDate.parse))
    }

    var installedCheckout: String? {
        guard let checkout, !checkout.isEmpty else { return nil }
        return checkout
    }

    /// The brain in words: "nim, model x", "manual", "none".
    var brain: String {
        switch verifier {
        case "nim": verifierModel.flatMap { $0.isEmpty ? nil : $0 } ?? "nim"
        case "manual": "a person (manual)"
        case "none", nil, "": "none"
        case let other?: other
        }
    }

    var vision: String {
        guard verifier == "nim" else { return "none" }
        return visionModel.flatMap { $0.isEmpty ? nil : $0 } ?? "none (the verifier does not see the screen)"
    }
}

// MARK: - update.sh

/// What `scripts/update.sh --check` printed: `main <sha>`, `ahead <n>`, a `commit <sha>
/// <subject>` line per commit main lacks (newest first) and `refused: <why>` when an update
/// would refuse. Other lines (git's own warnings) are ignored.
struct UpdateCheck: Equatable, Sendable {
    struct Commit: Equatable, Sendable, Identifiable {
        var sha: String
        var subject: String
        var id: String { sha }
    }

    var main: String
    var ahead: Int
    var commits: [Commit]
    var refusal: String?

    /// nil when the output has no `ahead` line: not update.sh, or it stopped early.
    static func parse(_ output: String) -> UpdateCheck? {
        var main = "", ahead: Int?, commits: [Commit] = [], refusal: String?
        for raw in output.split(separator: "\n", omittingEmptySubsequences: true) {
            let line = String(raw)
            if line.hasPrefix("main ") {
                main = String(line.dropFirst(5)).trimmingCharacters(in: .whitespaces)
            } else if line.hasPrefix("ahead ") {
                ahead = Int(line.dropFirst(6).trimmingCharacters(in: .whitespaces))
            } else if line.hasPrefix("commit ") {
                let rest = line.dropFirst(7)
                let parts = rest.split(separator: " ", maxSplits: 1, omittingEmptySubsequences: false)
                guard let sha = parts.first, !sha.isEmpty else { continue }
                commits.append(Commit(sha: String(sha), subject: parts.count > 1 ? String(parts[1]) : ""))
            } else if line.hasPrefix("refused: ") {
                refusal = String(line.dropFirst(9))
            }
        }
        guard let ahead else { return nil }
        return UpdateCheck(main: main, ahead: ahead, commits: commits, refusal: refusal)
    }
}

/// One update as its output tells it: `step: <name>` starts each step, `done:`,
/// `refused:` or `failed: <step>` ends it, and the exit status backs that up.
struct UpdateRun: Equatable, Sendable {
    enum Outcome: Equatable, Sendable {
        case running
        /// `done: main is at <sha>`: the Companion install then quits and reopens this app.
        case done(String)
        case refused(String)
        /// The step that failed, and the exit status.
        case failed(step: String, status: Int32)
    }

    /// A step as it started: its name and the index of its `step:` line.
    struct Step: Equatable, Sendable {
        var name: String
        var line: Int
    }

    /// Every line, in order.
    private(set) var lines: [String] = []
    /// The steps in the order they started.
    private(set) var steps: [Step] = []
    private(set) var outcome: Outcome = .running
    let started: Date

    init(started: Date = Date()) {
        self.started = started
    }

    var step: String? { steps.last?.name }
    var running: Bool { outcome == .running }

    mutating func take(_ line: String) {
        lines.append(line)
        if line.hasPrefix("step: ") {
            steps.append(Step(name: String(line.dropFirst(6)), line: lines.count - 1))
        } else if line.hasPrefix("done: ") {
            outcome = .done(String(line.dropFirst(6)))
        } else if line.hasPrefix("refused: ") {
            outcome = .refused(String(line.dropFirst(9)))
        } else if line.hasPrefix("failed: ") {
            outcome = .failed(step: String(line.dropFirst(8)), status: 1)
        }
    }

    /// The process ended with `status`. The script's own last line wins; with none (it was
    /// killed, or could not start), a non-zero status fails the step that was running.
    mutating func finish(status: Int32) {
        switch outcome {
        case .failed(let step, _):
            outcome = .failed(step: step, status: status == 0 ? 1 : status)
        case .running:
            outcome = status == 0 ? .done("") : .failed(step: step ?? "start the update", status: status)
        case .done, .refused:
            break
        }
    }

    /// The failing step's own output: the lines after its `step:` line, without the
    /// closing `failed:` line.
    var failingOutput: [String] {
        guard case .failed = outcome else { return [] }
        let from = (steps.last?.line).map { $0 + 1 } ?? 0
        return lines[min(from, lines.count)...].filter { !$0.hasPrefix("failed: ") }
    }
}

// MARK: - How the section reads

/// The Greenroom section's state in words (root ADR 0033): the headline, whether the app and
/// the daemon disagree, and whether Update can run and why not. Worked out from the facts
/// alone so every case has a test.
struct BuildsSummary: Equatable, Sendable {
    enum Check: Equatable, Sendable {
        case notYet
        case checking
        case checked(UpdateCheck, at: Date)
        case failed(String)
    }

    enum Headline: Equatable, Sendable {
        case checking
        case upToDate
        /// Commits on origin/main this checkout lacks.
        case updates(Int)
        /// The checkout is current but a build is not from it.
        case rebuild
        /// Nothing to compare with: why, in words.
        case unknown(String)
    }

    var headline: Headline
    /// The line under the headline, when it needs one.
    var detail: String?
    /// The app and the daemon were built from different commits.
    var mismatch: Bool
    /// Update can run now.
    var canUpdate: Bool
    /// Why Update is not offered, when it is not.
    var whyNot: String?

    static func of(app: AppBuild, daemon: DaemonVersion?, daemonIsLocal: Bool, checkout: String?,
                   check: Check, run: UpdateRun?) -> BuildsSummary {
        let daemonStamp = daemon?.stamp ?? .unknown
        let mismatch = app.stamp.known && daemonStamp.known && !app.stamp.sameCommit(as: daemonStamp)

        let headline: Headline
        var detail: String?
        switch check {
        case .notYet, .checking:
            headline = checkout == nil ? .unknown(noCheckout(daemonIsLocal: daemonIsLocal)) : .checking
        case .failed(let words):
            headline = .unknown("Could not check for updates: \(words)")
        case .checked(let result, _):
            if result.ahead > 0 {
                headline = .updates(result.ahead)
            } else if let stale = staleBuilds(app: app.stamp, daemon: daemonStamp, main: result.main) {
                headline = .rebuild
                detail = "\(stale). Main is at \(result.main). Update rebuilds both from it."
            } else {
                headline = .upToDate
            }
        }

        var whyNot: String?
        if run?.running == true {
            whyNot = "An update is running."
        } else if checkout == nil {
            whyNot = noCheckout(daemonIsLocal: daemonIsLocal)
        } else if case .checked(let result, _) = check, let refusal = result.refusal {
            whyNot = "Update would refuse: \(refusal)."
        } else if case .checked = check {
            if headline == .upToDate, !mismatch { whyNot = "Everything is built from main." }
        } else {
            whyNot = "Check for updates first."
        }
        if case .unknown(let why) = headline { detail = why }
        return BuildsSummary(headline: headline, detail: detail, mismatch: mismatch, canUpdate: whyNot == nil, whyNot: whyNot)
    }

    /// "greenroom is built from 4c3b2a1; the app has local changes", or nil when every
    /// stamped build is main as it is. An unstamped build (`swift run`) is not compared.
    private static func staleBuilds(app: BuildStamp, daemon: BuildStamp, main: String) -> String? {
        let words = [("greenroom", daemon), ("the app", app)].compactMap { name, stamp -> String? in
            guard stamp.known else { return nil }
            if !stamp.isCommit(main) { return "\(name) is built from \(stamp.commit)" }
            return stamp.dirty ? "\(name) has local changes" : nil
        }
        guard !words.isEmpty else { return nil }
        let joined = words.joined(separator: "; ")
        // "greenroom" stays lower case, as the product's name; "the app" starts a sentence.
        return joined.hasPrefix("the app") ? "T" + joined.dropFirst() : joined
    }

    private static func noCheckout(daemonIsLocal: Bool) -> String {
        daemonIsLocal
            ? "No source checkout is recorded. Install greenroom with its install scripts to update it here."
            : "greenroom runs on another Mac. Update it there."
    }

    var title: String {
        switch headline {
        case .checking: "Checking for updates"
        case .upToDate: "Up to date"
        case .updates(let n): n == 1 ? "1 update available" : "\(n) updates available"
        case .rebuild: "Not built from main"
        case .unknown: "Updates unknown"
        }
    }
}
