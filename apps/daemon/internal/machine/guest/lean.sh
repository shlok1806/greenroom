#!/bin/sh
# lean.sh: the "lean" image profile (variant A, docs/image-experiment/). Runs in the guest
# as the auto-logged-in admin user, through `greenroom prepare-image -lean`. It hides what
# an agent does not need and stops the background work and prompts that apps it never
# opens would start. It only writes preferences and launchd's disabled lists on the Data
# volume: SIP, authenticated root and the sealed system volume are left alone, so no app
# is deleted and every app stays launchable by path.
#
# Every setting is read back at the end. A key macOS renamed, or a disable launchd did
# not keep, fails the build with the name of the check.
set -eu

uid="$(id -u)"
fail=""
check() { # check <name> <command...>: record a failed read-back instead of stopping at the first
  name="$1"; shift
  if ! "$@" >/dev/null 2>&1; then
    fail="$fail $name"
    echo "lean: check failed: $name" >&2
  fi
}
is() { [ "$1" = "$2" ]; }

# --- Dock: only the core apps. Finder and Trash are always there. -----------------------
# Apps.app is the Launchpad of macOS 26; it lists every app on the sealed volume and has
# no per-app hide, so it leaves the Dock instead.
core_apps="/System/Applications/Utilities/Terminal.app
/System/Applications/System Settings.app
/Applications/Safari.app
/System/Applications/TextEdit.app
/System/Applications/Preview.app
/System/Applications/Utilities/Activity Monitor.app
/System/Applications/Utilities/Console.app"

defaults write com.apple.dock persistent-apps -array
echo "$core_apps" | while IFS= read -r app; do
  # The real path: /Applications/Safari.app is a link into the Safari cryptex, and a
  # link's tile wears an alias arrow.
  real="$(cd -P "$app" && pwd)"
  url="file://$(printf '%s/' "$real" | sed 's/ /%20/g')"
  defaults write com.apple.dock persistent-apps -array-add \
    "<dict><key>tile-data</key><dict><key>file-data</key><dict><key>_CFURLString</key><string>$url</string><key>_CFURLStringType</key><integer>15</integer></dict></dict></dict>"
done
defaults write com.apple.dock show-recents -bool false
defaults write com.apple.dock persistent-others -array
killall Dock 2>/dev/null || true

# --- User-level background agents of the apps that are gone, and of the features below ---
# launchctl disable persists in /private/var/db/com.apple.xpc.launchd (Data volume). A
# disabled label is neither started at login nor on demand. bootout stops it now.
agents="
com.apple.AMPArtworkAgent
com.apple.AMPDeviceDiscoveryAgent
com.apple.AMPDevicesAgent
com.apple.AMPLibraryAgent
com.apple.AMPSystemPlayerAgent
com.apple.amp.mediasharingd
com.apple.itunescloudd
com.apple.bookassetd
com.apple.bookdatastored
com.apple.newsd
com.apple.weatherd
com.apple.mobiletimerd
com.apple.calaccessd
com.apple.calendar.CalendarAgentBookmarkMigrationService
com.apple.AddressBook.SourceSync
com.apple.AddressBook.AssistantService
com.apple.contacts.donation-agent
com.apple.contacts.postersyncd
com.apple.remindd
com.apple.notes.exchangenotesd
com.apple.LinkedNotesUIService
com.apple.maps.destinationd
com.apple.Maps.mapspushd
com.apple.Maps.mapssyncd
com.apple.photoanalysisd
com.apple.photolibraryd
com.apple.cloudphotod
com.apple.mediaanalysisd
com.apple.mediastream.mstreamd
com.apple.generativeexperiencesd
com.apple.imagent
com.apple.imcore.imtransferagent
com.apple.facetimemessagestored
com.apple.telephonyutilities.callservicesd
com.apple.CallHistoryPluginHelper
com.apple.CallHistorySyncHelper
com.apple.callhistoryd
com.apple.callintelligenced
com.apple.screensharing.MessagesAgent
com.apple.email.maild
com.apple.icloudmailagent
com.apple.homed
com.apple.homeenergyd
com.apple.homeeventsd
com.apple.gamed
com.apple.GameOverlayUI
com.apple.GamePolicyAgent
com.apple.gamesaved
com.apple.GameController.gamecontrolleragentd
com.apple.tipsd
com.apple.voicememod
com.apple.stickersd
com.apple.Safari.PasswordBreachAgent
com.apple.Siri.agent
com.apple.assistantd
com.apple.assistant_cdmd
com.apple.assistant_service
com.apple.siriactionsd
com.apple.siriinferenced
com.apple.siriknowledged
com.apple.sirittsd
com.apple.SiriTTSTrainingAgent
com.apple.corespeechd
com.apple.spotlightknowledged
com.apple.spotlightknowledged.importer
com.apple.spotlightknowledged.updater
com.apple.chronod
com.apple.notificationcenterui.agent
com.apple.followupd
com.apple.FollowUpUI
com.apple.iCloudNotificationAgent
com.apple.iCloudUserNotificationsd
com.apple.SoftwareUpdateNotificationManager
"
printf '%s\n' "$agents" | while IFS= read -r label; do
  [ -n "$label" ] || continue
  launchctl disable "gui/$uid/$label"
  launchctl bootout "gui/$uid/$label" 2>/dev/null || true
done

# --- Desktop widgets off (WindowManager hosts them on the desktop and in Stage Manager). --
defaults write com.apple.WindowManager StandardHideWidgets -bool true
defaults write com.apple.WindowManager StageManagerHideWidgets -bool true

# --- Siri off, and out of the menu bar. ---------------------------------------------------
defaults write com.apple.assistant.support "Assistant Enabled" -bool false
defaults write com.apple.Siri StatusMenuVisible -bool false
defaults write com.apple.Siri VoiceTriggerUserEnabled -bool false

# --- Spotlight indexing off on every volume (the index lives on the Data volume). --------
sudo -n mdutil -a -i off >/dev/null 2>&1

# --- Game Center off. ---------------------------------------------------------------------
defaults write com.apple.gamed Disabled -bool true

# --- Software Update: no checks, downloads or installs, and no App Store auto updates. ----
sudo -n softwareupdate --schedule off >/dev/null 2>&1 || true
for key in AutomaticCheckEnabled AutomaticDownload AutomaticallyInstallMacOSUpdates CriticalUpdateInstall ConfigDataInstall; do
  sudo -n defaults write /Library/Preferences/com.apple.SoftwareUpdate "$key" -bool false
done
sudo -n defaults write /Library/Preferences/com.apple.commerce AutoUpdate -bool false

# --- Time Machine: never offer a new disk, and no backups. --------------------------------
sudo -n defaults write /Library/Preferences/com.apple.TimeMachine DoNotOfferNewDisksForBackup -bool true
sudo -n tmutil disable 2>/dev/null || true

# --- Setup Assistant, What's New and Apple Account prompts: every pane already seen for
# this build, so neither login nor an update relaunches Setup Assistant (MiniBuddy). ------
product="$(sw_vers -productVersion)"
build="$(sw_vers -buildVersion)"
for key in DidSeeAccessibility DidSeeActivationLock DidSeeAppStore DidSeeAppearanceSetup \
  DidSeeApplePaySetup DidSeeCloudSetup DidSeeIntelligence DidSeeLockdownMode DidSeePrivacy \
  DidSeeScreenTime DidSeeSiriSetup DidSeeSyncSetup DidSeeSyncSetup2 DidSeeTermsOfAddress \
  DidSeeTouchIDSetup DidSeeiCloudLoginForStorageServices; do
  defaults write com.apple.SetupAssistant "$key" -bool true
done
for key in LastSeenCloudProductVersion LastSeenSiriProductVersion LastSeenDiagnosticsProductVersion \
  LastSeenIntelligenceProductVersion LastSeenAgeRangeSelectionProductVersion; do
  defaults write com.apple.SetupAssistant "$key" -string "$product"
done
defaults write com.apple.SetupAssistant LastSeenBuddyBuildVersion -string "$build"
defaults write com.apple.SetupAssistant MiniBuddyShouldLaunchToResumeSetup -bool false
sudo -n touch /var/db/.AppleSetupDone

# --- Read back. ---------------------------------------------------------------------------
dock="$(defaults read com.apple.dock persistent-apps)"
dock_count="$(printf '%s\n' "$dock" | grep -c '_CFURLString"' || true)"
check dock-count is "$dock_count" 7
for app in Terminal System%20Settings Safari TextEdit Preview Activity%20Monitor Console; do
  check "dock-$app" sh -c "printf '%s' \"\$1\" | grep -q \"/$app.app\"" _ "$dock"
done
check dock-recents is "$(defaults read com.apple.dock show-recents)" 0

# One label a line, exactly as launchd reports it, so a label with stray whitespace fails.
disabled="$(launchctl print-disabled "gui/$uid" | sed -n 's/^[[:space:]]*"\([^"]*\)" => disabled$/\1/p')"
missing="$(printf '%s\n' "$agents" | while IFS= read -r label; do
  [ -z "$label" ] || printf '%s\n' "$disabled" | grep -qxF "$label" || printf ' %s' "$label"
done)"
check "disabled-agents${missing:+:$missing}" is "$missing" ""
check widgets-desktop is "$(defaults read com.apple.WindowManager StandardHideWidgets)" 1
check widgets-stage is "$(defaults read com.apple.WindowManager StageManagerHideWidgets)" 1
check siri-enabled is "$(defaults read com.apple.assistant.support 'Assistant Enabled')" 0
check siri-menu is "$(defaults read com.apple.Siri StatusMenuVisible)" 0
check spotlight sh -c '! mdutil -s /System/Volumes/Data | grep -q "Indexing enabled"'
check gamecenter is "$(defaults read com.apple.gamed Disabled)" 1
for key in AutomaticCheckEnabled AutomaticDownload AutomaticallyInstallMacOSUpdates CriticalUpdateInstall ConfigDataInstall; do
  check "softwareupdate-$key" is "$(defaults read /Library/Preferences/com.apple.SoftwareUpdate "$key")" 0
done
check appstore-autoupdate is "$(defaults read /Library/Preferences/com.apple.commerce AutoUpdate)" 0
check timemachine-offer is "$(defaults read /Library/Preferences/com.apple.TimeMachine DoNotOfferNewDisksForBackup)" 1
check setup-cloud is "$(defaults read com.apple.SetupAssistant DidSeeCloudSetup)" 1
check setup-buddy-build is "$(defaults read com.apple.SetupAssistant LastSeenBuddyBuildVersion)" "$build"
check setup-done sudo -n test -f /var/db/.AppleSetupDone

if [ -n "$fail" ]; then
  echo "lean: failed:$fail" >&2
  exit 1
fi
echo "lean: ok"
