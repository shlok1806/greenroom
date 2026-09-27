import XCTest

@testable import Companion

/// The builds and updates rules (root ADR 0033): stamps, the daemon's `/api/version`, what
/// `update.sh` prints, and how the Greenroom section reads them.
final class BuildsTests: XCTestCase {
    // MARK: - Stamps

    func testTheAppReadsTheStampBundleShWrites() {
        let app = AppBuild.from(info: [
            "GreenroomCommit": "abc1234", "GreenroomDirty": true,
            "GreenroomBuiltAt": "2026-09-27T10:00:00Z", "GreenroomCheckout": "/src/greenroom",
        ])
        XCTAssertEqual(app.stamp.commit, "abc1234")
        XCTAssertTrue(app.stamp.dirty)
        XCTAssertEqual(app.stamp.builtAt, DaemonDate.parse("2026-09-27T10:00:00Z"))
        XCTAssertEqual(app.checkout, "/src/greenroom")
        XCTAssertEqual(app.stamp.label, "abc1234 + local changes")
    }

    func testAnUnbundledAppHasNoStamp() {
        let app = AppBuild.from(info: ["CFBundleName": "Companion"])
        XCTAssertFalse(app.stamp.known)
        XCTAssertNil(app.checkout)
        XCTAssertEqual(app.stamp.label, "not stamped")
        XCTAssertFalse(AppBuild.from(info: nil).stamp.known)
        XCTAssertNil(AppBuild.from(info: ["GreenroomCheckout": ""]).checkout)
    }

    func testShortShasOfDifferentLengthsMatch() {
        let stamp = BuildStamp(commit: "abc1234")
        XCTAssertTrue(stamp.isCommit("abc1234"))
        XCTAssertTrue(stamp.isCommit("ABC12345"))
        XCTAssertTrue(stamp.isCommit("abc12"))
        XCTAssertFalse(stamp.isCommit("abd1234"))
        XCTAssertFalse(stamp.isCommit(""))
        XCTAssertFalse(BuildStamp.unknown.isCommit("abc1234"))
        XCTAssertFalse(BuildStamp.unknown.sameCommit(as: .unknown))
    }

    // MARK: - The daemon's version

    /// The shape `apps/daemon/internal/api/version_test.go` pins.
    func testTheDaemonsVersionDecodes() throws {
        let json = #"""
        {"version":"0.0.2","commit":"abc1234","dirty":true,"builtAt":"2026-09-27T10:00:00Z","inputHelper":7,"imageRecipe":2,
         "verifier":"nim","verifierModel":"brain/model","visionModel":"vision/model","checkout":"/src/greenroom"}
        """#
        let version = try JSONDecoder.daemon().decode(DaemonVersion.self, from: Data(json.utf8))
        XCTAssertEqual(version.stamp, BuildStamp(commit: "abc1234", dirty: true, builtAt: DaemonDate.parse("2026-09-27T10:00:00Z")))
        XCTAssertEqual(version.inputHelper, 7)
        XCTAssertEqual(version.imageRecipe, 2)
        XCTAssertEqual(version.brain, "brain/model")
        XCTAssertEqual(version.vision, "vision/model")
        XCTAssertEqual(version.installedCheckout, "/src/greenroom")
    }

    func testAnUnstampedOrPartialVersionStillDecodes() throws {
        let version = try JSONDecoder.daemon().decode(DaemonVersion.self, from: Data(
            #"{"commit":"","dirty":false,"builtAt":"","verifier":"none","checkout":""}"#.utf8))
        XCTAssertFalse(version.stamp.known)
        XCTAssertNil(version.stamp.builtAt)
        XCTAssertNil(version.installedCheckout)
        XCTAssertEqual(version.brain, "none")
        XCTAssertEqual(version.vision, "none")
        let empty = try JSONDecoder.daemon().decode(DaemonVersion.self, from: Data("{}".utf8))
        XCTAssertFalse(empty.stamp.known)
        XCTAssertEqual(DaemonVersion(verifier: "manual").brain, "a person (manual)")
        XCTAssertEqual(DaemonVersion(verifier: "nim", visionModel: "").vision, "none (the verifier does not see the screen)")
    }

    func testTheClientAsksTheVersionRoute() async throws {
        let seen = Recorder()
        let client = StubURLProtocol.client { request in
            seen.append(request.url?.path ?? "")
            return .json(#"{"commit":"abc1234"}"#)
        }
        let version = try await client.version()
        XCTAssertEqual(version.commit, "abc1234")
        XCTAssertEqual(seen.values, ["/api/version"])
    }

    func testOnlyALoopbackDaemonIsLocal() {
        let local = ["http://127.0.0.1:7777", "http://localhost:7777", "http://[::1]:7777", "http://127.0.0.2:9"]
        for url in local {
            XCTAssertTrue(DaemonClient(baseURL: URL(string: url)!).isLocal, url)
        }
        for url in ["https://greenroom.example.com", "http://192.168.1.4:7777", "http://daemon.test"] {
            XCTAssertFalse(DaemonClient(baseURL: URL(string: url)!).isLocal, url)
        }
    }

    // MARK: - update.sh --check

    func testACheckWithNothingNew() {
        XCTAssertEqual(UpdateCheck.parse("main abc1234\nahead 0\n"),
                       UpdateCheck(main: "abc1234", ahead: 0, commits: [], refusal: nil))
    }

    func testACheckListsTheCommitsNewestFirst() {
        let output = """
        warning: redirecting to https://github.com/shlok1806/greenroom.git/
        main 9f8e7d6
        ahead 2
        commit 9f8e7d6 companion: the builds section (ADR 0033)
        commit 1a2b3c4 verifier: retry unreadable descriptions
        refused: the checkout at /src/greenroom has local changes; commit, stash or remove them first
        """
        let check = UpdateCheck.parse(output)
        XCTAssertEqual(check?.main, "9f8e7d6")
        XCTAssertEqual(check?.ahead, 2)
        XCTAssertEqual(check?.commits.map(\.sha), ["9f8e7d6", "1a2b3c4"])
        XCTAssertEqual(check?.commits.first?.subject, "companion: the builds section (ADR 0033)")
        XCTAssertEqual(check?.refusal, "the checkout at /src/greenroom has local changes; commit, stash or remove them first")
    }

    func testOutputWithNoCountIsNotACheck() {
        XCTAssertNil(UpdateCheck.parse(""))
        XCTAssertNil(UpdateCheck.parse("failed: fetch origin\n"))
        XCTAssertNil(UpdateCheck.parse("ahead lots\n"))
    }

    // MARK: - An update's output

    private func run(_ lines: [String], status: Int32? = nil) -> UpdateRun {
        var run = UpdateRun(started: Date(timeIntervalSince1970: 0))
        for line in lines { run.take(line) }
        if let status { run.finish(status: status) }
        return run
    }

    func testAnUpdateFollowsItsSteps() {
        let going = run(["step: check the checkout", "step: fetch origin", "step: fast-forward main (2 new commits)", "step: install the daemon", "building"])
        XCTAssertTrue(going.running)
        XCTAssertEqual(going.step, "install the daemon")
        XCTAssertEqual(going.steps.map(\.name), ["check the checkout", "fetch origin", "fast-forward main (2 new commits)", "install the daemon"])
        let done = run(going.lines + ["step: install the Companion", "done: main is at 9f8e7d6"], status: 0)
        XCTAssertEqual(done.outcome, .done("main is at 9f8e7d6"))
        XCTAssertTrue(done.failingOutput.isEmpty)
    }

    func testAFailedUpdateNamesTheStepAndKeepsItsOutput() {
        let failed = run([
            "step: check the checkout", "step: fetch origin", "step: fast-forward main (1 new commits)",
            "step: install the daemon", "building /Users/me/.greenroom/bin/greenroom", "main.go:3: syntax error",
            "failed: install the daemon",
        ], status: 1)
        XCTAssertEqual(failed.outcome, .failed(step: "install the daemon", status: 1))
        XCTAssertEqual(failed.failingOutput, ["building /Users/me/.greenroom/bin/greenroom", "main.go:3: syntax error"])
    }

    func testARefusalSaysWhy() {
        let refused = run(["step: check the checkout", "refused: the checkout at /src is not on main"], status: 2)
        XCTAssertEqual(refused.outcome, .refused("the checkout at /src is not on main"))
    }

    /// Killed, or it could not say: the status decides, at the step that was running.
    func testAnUpdateThatEndsWithoutAWordFailsAtItsStep() {
        XCTAssertEqual(run(["step: install the Companion", "quit the running app"], status: 143).outcome,
                       .failed(step: "install the Companion", status: 143))
        XCTAssertEqual(run([], status: 127).outcome, .failed(step: "start the update", status: 127))
        XCTAssertEqual(run(["step: fetch origin"], status: 0).outcome, .done(""))
        XCTAssertEqual(run(["failed: fetch origin"], status: 0).outcome, .failed(step: "fetch origin", status: 1))
    }

    // MARK: - How the section reads

    private let main = "9f8e7d6"
    private func app(_ commit: String = "9f8e7d6", dirty: Bool = false) -> AppBuild {
        AppBuild(stamp: BuildStamp(commit: commit, dirty: dirty), checkout: "/src/greenroom")
    }
    private func daemon(_ commit: String = "9f8e7d6", dirty: Bool = false) -> DaemonVersion {
        DaemonVersion(commit: commit, dirty: dirty, checkout: "/src/greenroom")
    }
    private func checked(ahead: Int = 0, refusal: String? = nil) -> BuildsSummary.Check {
        .checked(UpdateCheck(main: main, ahead: ahead, commits: [], refusal: refusal), at: Date(timeIntervalSince1970: 0))
    }
    private func summary(app: AppBuild? = nil, daemon: DaemonVersion? = nil, local: Bool = true,
                         checkout: String? = "/src/greenroom", check: BuildsSummary.Check, run: UpdateRun? = nil) -> BuildsSummary {
        BuildsSummary.of(app: app ?? self.app(), daemon: daemon ?? self.daemon(), daemonIsLocal: local, checkout: checkout, check: check, run: run)
    }

    func testUpToDateOffersNoUpdate() {
        let s = summary(check: checked())
        XCTAssertEqual(s.headline, .upToDate)
        XCTAssertNil(s.detail)
        XCTAssertEqual(s.title, "Up to date")
        XCTAssertFalse(s.mismatch)
        XCTAssertFalse(s.canUpdate)
        XCTAssertEqual(s.whyNot, "Everything is built from main.")
    }

    func testCommitsOnMainAreUpdates() {
        let s = summary(app: app("1a2b3c4"), daemon: daemon("1a2b3c4"), check: checked(ahead: 3))
        XCTAssertEqual(s.headline, .updates(3))
        XCTAssertEqual(s.title, "3 updates available")
        XCTAssertTrue(s.canUpdate)
        XCTAssertNil(s.whyNot)
        XCTAssertEqual(summary(check: checked(ahead: 1)).title, "1 update available")
    }

    /// The stale daemon of 2026-09-26: the checkout was current, the running build was not.
    func testACurrentCheckoutWithAnOldBuildNeedsARebuild() {
        let s = summary(daemon: daemon("1a2b3c4"), check: checked())
        XCTAssertEqual(s.headline, .rebuild)
        XCTAssertEqual(s.detail, "greenroom is built from 1a2b3c4. Main is at 9f8e7d6. Update rebuilds both from it.")
        XCTAssertEqual(summary(app: app(dirty: true), daemon: daemon("1a2b3c4"), check: checked()).detail,
                       "greenroom is built from 1a2b3c4; the app has local changes. Main is at 9f8e7d6. Update rebuilds both from it.")
        XCTAssertTrue(s.mismatch)
        XCTAssertTrue(s.canUpdate)
        XCTAssertEqual(summary(app: app(dirty: true), check: checked()).headline, .rebuild)
        XCTAssertEqual(summary(app: app(dirty: true), check: checked()).detail,
                       "The app has local changes. Main is at 9f8e7d6. Update rebuilds both from it.")
        // An unstamped build (swift run) is not compared.
        XCTAssertEqual(summary(app: AppBuild(stamp: .unknown), check: checked()).headline, .upToDate)
    }

    func testTheAppAndTheDaemonFromDifferentCommitsIsAMismatch() {
        XCTAssertTrue(summary(app: app("1a2b3c4"), check: checked(ahead: 2)).mismatch)
        XCTAssertFalse(summary(app: app("9f8e7d6aa"), check: checked()).mismatch)
        XCTAssertFalse(summary(app: AppBuild(stamp: .unknown), check: checked()).mismatch)
        XCTAssertFalse(BuildsSummary.of(app: app("1a2b3c4"), daemon: nil, daemonIsLocal: true, checkout: "/src",
                                        check: checked(), run: nil).mismatch)
    }

    func testARefusalIsWhyUpdateIsNotOffered() {
        let s = summary(check: checked(ahead: 2, refusal: "the checkout at /src is not on main"))
        XCTAssertEqual(s.headline, .updates(2))
        XCTAssertFalse(s.canUpdate)
        XCTAssertEqual(s.whyNot, "Update would refuse: the checkout at /src is not on main.")
    }

    func testNoCheckoutNoUpdate() {
        let local = summary(checkout: nil, check: .notYet)
        XCTAssertEqual(local.headline, .unknown("No source checkout is recorded. Install greenroom with its install scripts to update it here."))
        XCTAssertFalse(local.canUpdate)
        let remote = summary(local: false, checkout: nil, check: .notYet)
        XCTAssertEqual(remote.whyNot, "greenroom runs on another Mac. Update it there.")
        XCTAssertEqual(remote.detail, "greenroom runs on another Mac. Update it there.")
    }

    func testCheckingAndAFailedCheck() {
        let checking = summary(check: .checking)
        XCTAssertEqual(checking.headline, .checking)
        XCTAssertFalse(checking.canUpdate)
        let failed = summary(check: .failed("failed: fetch origin"))
        XCTAssertEqual(failed.headline, .unknown("Could not check for updates: failed: fetch origin"))
        XCTAssertFalse(failed.canUpdate)
    }

    func testNothingStartsWhileAnUpdateRuns() {
        var running = UpdateRun()
        running.take("step: install the daemon")
        let s = summary(check: checked(ahead: 2), run: running)
        XCTAssertFalse(s.canUpdate)
        XCTAssertEqual(s.whyNot, "An update is running.")
    }
}
