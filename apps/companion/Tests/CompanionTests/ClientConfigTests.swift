import XCTest

@testable import Companion

final class ClientConfigTests: XCTestCase {
    private var home: URL!

    override func setUpWithError() throws {
        home = FileManager.default.temporaryDirectory.appending(component: "client-config-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: home.appending(component: ".greenroom"), withIntermediateDirectories: true)
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: home)
    }

    private func write(_ text: String) throws {
        try Data(text.utf8).write(to: ClientConfig.file(home: home))
    }

    private func resolve(_ environment: [String: String] = [:]) -> ClientConfig {
        ClientConfig.resolve(environment: environment, file: ClientConfig.file(home: home))
    }

    func testTheFileLivesInDotGreenroom() {
        XCTAssertEqual(ClientConfig.file(home: URL(filePath: "/Users/a")).path(), "/Users/a/.greenroom/client.json")
    }

    func testNothingConfiguredIsLoopbackWithNoToken() {
        XCTAssertEqual(resolve(), ClientConfig(baseURL: URL(string: "http://127.0.0.1:7777")!, token: nil))
    }

    func testTheFileGivesTheAddressAndToken() throws {
        try write(#"{"url": "https://gr.example.com", "token": "abc", "installedAt": "2026-09-25"}"#)
        XCTAssertEqual(resolve(), ClientConfig(baseURL: URL(string: "https://gr.example.com")!, token: "abc"))
    }

    func testTheEnvironmentWinsOverTheFileAndNeverTakesItsToken() throws {
        try write(#"{"url": "https://gr.example.com", "token": "abc"}"#)
        XCTAssertEqual(resolve(["GREENROOM_URL": "http://127.0.0.1:7851"]),
                       ClientConfig(baseURL: URL(string: "http://127.0.0.1:7851")!, token: nil))
        XCTAssertEqual(resolve(["GREENROOM_URL": "http://127.0.0.1:7851", "GREENROOM_TOKEN": "env"]),
                       ClientConfig(baseURL: URL(string: "http://127.0.0.1:7851")!, token: "env"))
    }

    func testATokenAloneInTheEnvironmentChangesNothing() throws {
        XCTAssertEqual(resolve(["GREENROOM_TOKEN": "env"]), .loopback)
        try write(#"{"url": "https://gr.example.com", "token": "abc"}"#)
        XCTAssertEqual(resolve(["GREENROOM_TOKEN": "env"]).token, "abc")
    }

    func testAnEmptyTokenIsNoToken() throws {
        try write(#"{"url": "https://gr.example.com", "token": " "}"#)
        XCTAssertNil(resolve().token)
        XCTAssertNil(resolve(["GREENROOM_URL": "http://127.0.0.1:1", "GREENROOM_TOKEN": ""]).token)
    }

    func testAMalformedFileFallsBackToLoopback() throws {
        for text in ["", "not json", #"{"token": "abc"}"#, #"{"url": 7}"#, #"{"url": "gr.example.com"}"#, "[]"] {
            try write(text)
            XCTAssertEqual(resolve(), .loopback, text)
        }
    }

    func testAnUnusableEnvironmentAddressFallsThroughToTheFile() throws {
        try write(#"{"url": "https://gr.example.com", "token": "abc"}"#)
        XCTAssertEqual(resolve(["GREENROOM_URL": "not a url"]).baseURL, URL(string: "https://gr.example.com")!)
    }
}
