// Host tests for logic/ (daemon ADR 0005), run by swift_test.go. Each test is a function in a
// file of this directory, listed in `tests` below; `expect` records a failure without stopping.

import Foundation

var failures: [String] = []
var current = ""

func expect(_ condition: @autoclosure () -> Bool, _ message: @autoclosure () -> String,
            file: String = #fileID, line: Int = #line) {
    if !condition() {
        failures.append("\(current): \(file):\(line): \(message())")
    }
}

func expectEqual<T: Equatable>(_ got: T, _ want: T, _ what: String = "", file: String = #fileID, line: Int = #line) {
    if got != want {
        failures.append("\(current): \(file):\(line): \(what.isEmpty ? "" : what + ": ")got \(got), want \(want)")
    }
}

let tests: [(String, () -> Void)] = [
    ("cut keeps a short string whole", testCutKeepsAShortStringWhole),
    ("cut marks a long string", testCutMarksALongString),
    ("a frame round trips", testAFrameRoundTrips),
    ("a frame over the limit is refused before its payload", testAFrameOverTheLimitIsRefusedBeforeItsPayload),
    ("a truncated frame throws", testATruncatedFrameThrows),
    ("frame types match the ADR", testFrameTypesMatchTheADR),
    ("a blob splits into numbered chunks", testABlobSplitsIntoNumberedChunks),
    ("a blob of exactly one chunk and an empty blob", testABlobOfExactlyOneChunkAndAnEmptyBlob),
    ("a request decodes", testARequestDecodes),
    ("a request without args has an empty object", testARequestWithoutArgsHasAnEmptyObject),
    ("a malformed request is answered when it has an id", testAMalformedRequestIsAnsweredWhenItHasAnID),
    ("deadlines are bounded", testDeadlinesAreBounded),
    ("only the ADR's codes retry", testOnlyTheADRsCodesRetry),
    ("a decoding error names the field", testADecodingErrorNamesTheField),
    ("a rect argument needs four numbers and a size", testARectArgumentNeedsFourNumbersAndASize),
    ("a region becomes pixels rounded outward", testARegionBecomesPixelsRoundedOutward),
    ("a region is clipped to the image", testARegionIsClippedToTheImage),
    ("an image is scaled down to maxWidth only", testAnImageIsScaledDownToMaxWidthOnly),
    ("a stall is reported once and its recovery", testAStallIsReportedOnceAndItsRecovery),
    ("kinds stall apart", testKindsStallApart),
    ("a stall lasts until its longest work ends", testAStallLastsUntilItsLongestWorkEnds),
    ("a tail buffer keeps the end", testATailBufferKeepsTheEnd),
    ("an element keeps its ref across walks", testAnElementKeepsItsRefAcrossWalks),
    ("a ref is never reused", testARefIsNeverReused),
    ("the least recently seen refs go first", testTheLeastRecentlySeenRefsGoFirst),
    ("a table never holds more than its capacity", testATableNeverHoldsMoreThanItsCapacity),
    ("readers have tables of their own", testReadersHaveTablesOfTheirOwn),
    ("a ref is rebound to the element its fingerprint found", testARefIsReboundToTheElementItsFingerprintFound),
    ("only refs parse", testOnlyRefsParse),
    ("an identifier finds its element wherever it moved", testAnIdentifierFindsItsElementWhereverItMoved),
    ("an identifier on another role is not the element", testAnIdentifierOnAnotherRoleIsNotTheElement),
    ("rows that share an identifier are told apart by place and name", testRowsThatShareAnIdentifierAreToldApartByPlaceAndName),
    ("without an identifier path, role and name must all match", testWithoutAnIdentifierPathRoleAndNameMustAllMatch),
    ("two elements that fit are no match", testTwoElementsThatFitAreNoMatch),
    ("AppKit's generated identifiers are dropped", testAppKitsGeneratedIdentifiersAreDropped),
    ("a role path grows by role and index", testARolePathGrowsByRoleAndIndex),
    ("a frame in view is wholly visible", testAFrameInViewIsWhollyVisible),
    ("a frame is clipped through nested scroll areas", testAFrameIsClippedThroughNestedScrollAreas),
    ("an element out of its scroll area is offscreen and says which way", testAnElementOutOfItsScrollAreaIsOffscreenAndSaysWhichWay),
    ("offscreen names the scroll area that hides it", testOffscreenNamesTheScrollAreaThatHidesIt),
    ("an element outside its window is hidden unless a scroll area holds it", testAnElementOutsideItsWindowIsHiddenUnlessAScrollAreaHoldsIt),
    ("an element with no area is not listed", testAnElementWithNoAreaIsNotListed),
    ("clipped names what cut the frame", testClippedNamesWhatCutTheFrame),
    ("a hit on the element, its inside or its outside covers nothing", testAHitOnTheElementItsInsideOrItsOutsideCoversNothing),
    ("a coverer is named by its nearest listed ancestor", testACovererIsNamedByItsNearestListedAncestor),
    ("a toolbar item behind the chevron is in overflow", testAToolbarItemBehindTheChevronIsInOverflow),
    ("a sheet is a surface of its own", testASheetIsASurfaceOfItsOwn),
    ("a scroll bar's value is the position", testAScrollBarsValueIsThePosition),
    ("without scroll bars the content's frame is the position", testWithoutScrollBarsTheContentsFrameIsThePosition),
    ("content that fits does not scroll", testContentThatFitsDoesNotScroll),
    ("the scroll bar wins over the rows a lazy list has made", testTheScrollBarWinsOverTheRowsALazyListHasMade),
    ("a scroll area's content is the union of its children", testAScrollAreasContentIsTheUnionOfItsChildren),
    ("a name is the title, then the description, then the placeholder", testANameIsTheTitleThenTheDescriptionThenThePlaceholder),
    ("roles lose their prefix on the wire", testRolesLoseTheirPrefixOnTheWire),
    ("a long string is cut and marked", testALongStringIsCutAndMarked),
    ("fullText lifts the limit", testFullTextLiftsTheLimit),
    ("a secure field never sends its value", testASecureFieldNeverSendsItsValue),
    ("interactive mode lists controls, text and named containers", testInteractiveModeListsControlsTextAndNamedContainers),
    ("containers are not hit-tested", testContainersAreNotHitTested),
    ("windows over the target are named front to back", testWindowsOverTheTargetAreNamedFrontToBack),
    ("a notification over the target counts", testANotificationOverTheTargetCounts),
    ("the frontmost app's windows cover a background target", testTheFrontmostAppsWindowsCoverABackgroundTarget),
    ("transparent, offscreen and own windows are skipped", testTransparentOffscreenAndOwnWindowsAreSkipped),
    ("the list of windows over a target is bounded", testTheListOfWindowsOverATargetIsBounded),
    ("open menus are the target's windows at the menu level", testOpenMenusAreTheTargetsWindowsAtTheMenuLevel),
    ("the window server's list becomes values", testTheWindowServersListBecomesValues),
    ("dialogs, sheets and popovers are attention", testDialogsSheetsAndPopoversAreAttention),
    ("plain text is found anywhere whatever the case", testPlainTextIsFoundAnywhereWhateverTheCase),
    ("text between slashes is a regular expression", testTextBetweenSlashesIsARegularExpression),
    ("a bad regular expression says so", testABadRegularExpressionSaysSo),
    ("a role matches with or without its prefix", testARoleMatchesWithOrWithoutItsPrefix),
    ("find looks in every text field but a secret", testFindLooksInEveryTextFieldButASecret),
    ("a signature changes with what it read", testASignatureChangesWithWhatItRead),
]

for (name, test) in tests {
    current = name
    test()
}
for failure in failures {
    print("FAIL \(failure)")
}
if failures.isEmpty {
    print("\(tests.count) tests passed")
    exit(0)
}
print("\(failures.count) failures in \(tests.count) tests")
exit(1)
