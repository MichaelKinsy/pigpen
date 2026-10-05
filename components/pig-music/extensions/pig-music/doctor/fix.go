package doctor

// The fixes are one copy-paste line per operating system. They are what the maintainers of each program document; the
// Windows winget identifiers have not been run on a Windows machine here.

func osName(goos string) string {
	switch goos {
	case "darwin", "windows", "android":
		return goos
	}
	return "linux"
}

func fixMPV(goos string) string {
	switch osName(goos) {
	case "darwin":
		return "brew install mpv"
	case "windows":
		return "winget install shinchiro.mpv"
	case "android":
		return "pkg install mpv"
	}
	return "sudo apt install mpv   (or: sudo dnf install mpv, sudo pacman -S mpv)"
}

func fixYtdlp(goos string) string {
	switch osName(goos) {
	case "darwin":
		return "brew install yt-dlp   (or run /music setup to download the official release)"
	case "windows":
		return "winget install yt-dlp.yt-dlp   (or run /music setup to download the official release)"
	case "android":
		return "pkg install python-yt-dlp"
	}
	return "run /music setup to download the official yt-dlp release   (or: pipx install \"yt-dlp[default]\")"
}

func fixYtdlpUpdate(goos string, selfManaged bool) string {
	if selfManaged {
		return "run /music setup (pig-music updates its own copy)"
	}
	switch osName(goos) {
	case "darwin":
		return "brew upgrade yt-dlp   (or: yt-dlp -U for a release build)"
	case "windows":
		return "winget upgrade yt-dlp.yt-dlp   (or: yt-dlp -U)"
	case "android":
		return "pkg upgrade python-yt-dlp"
	}
	return "yt-dlp -U   (release builds), pipx upgrade yt-dlp, or run /music setup for a copy pig-music keeps current"
}

func fixJS(goos string) string {
	switch osName(goos) {
	case "darwin":
		return "brew install deno   (or run /music setup to download Deno)"
	case "windows":
		return "winget install DenoLand.Deno   (or run /music setup to download Deno)"
	case "android":
		return "pkg install nodejs   (then set \"jsRuntime\": \"node\" in the pig-music settings)"
	}
	return "run /music setup to download Deno   (or install Node 22 or later and set \"jsRuntime\": \"node\")"
}

func fixEJS(goos string) string {
	switch osName(goos) {
	case "android":
		return "pkg upgrade python-yt-dlp"
	}
	return "install yt-dlp with its default extras (pipx install \"yt-dlp[default]\"), or use the official release, which includes yt-dlp-ejs"
}

func fixAudio(goos string) string {
	if goos == "android" {
		return "pulseaudio --start --exit-idle-time=-1 && pactl load-module module-aaudio-sink   (Android 16 needs module-aaudio-sink)"
	}
	return "sudo apt install pipewire-pulse   (or: sudo apt install libasound2)"
}
