package clientcatalog

func DefaultDefinitions() []Definition {
	return []Definition{
		{
			ID: "karing", Name: "Karing", Core: "Sing-box",
			Description: "基于 Sing-box 的多平台客户端；HWID 需要在客户端中启用。",
			Downloads: []DownloadDefinition{
				repository("android", "KaringX/karing", `(?i)_android_arm\.apk$`, `(?i)_android_arm64-v8a\.apk$`),
				external("ios", "app-store", "https://apps.apple.com/us/app/karing/id6472431552"),
				repository("macos", "KaringX/karing", `(?i)_macos_universal\.dmg$`),
				repository("windows", "KaringX/karing", `(?i)_windows_x64\.exe$`),
				repository("linux", "KaringX/karing", `(?i)_linux_amd64\.AppImage$`, `(?i)_linux_amd64\.deb$`),
			},
		},
		{
			ID: "happ", Name: "Happ", Core: "Xray", Featured: true,
			Description: "支持 Android、iOS、macOS、Windows 与 Linux 的多平台客户端。",
			Downloads: []DownloadDefinition{
				external("android", "github", "https://github.com/Happ-proxy/happ-android/releases/latest/download/Happ.apk"),
				external("ios", "app-store", "https://apps.apple.com/us/app/happ-proxy-utility/id6504287215"),
				repository("macos", "Happ-proxy/happ-desktop", `(?i)\.macOS\.universal\.dmg$`),
				repository("windows", "Happ-proxy/happ-desktop", `(?i)setup-Happ\.x64\.exe$`),
				repository("linux", "Happ-proxy/happ-desktop", `(?i)\.linux\.x64\.deb$`),
			},
		},
		{
			ID: "clash-mi", Name: "Clash Mi", Core: "Mihomo",
			Description: "跨平台 Mihomo 客户端；HWID 需要在客户端中启用。",
			Downloads: []DownloadDefinition{
				repository("android", "KaringX/clashmi", `(?i)_android_arm\.apk$`, `(?i)_android_arm64-v8a\.apk$`),
				external("ios", "app-store", "https://apps.apple.com/us/app/clash-mi/id6744321968"),
				repository("macos", "KaringX/clashmi", `(?i)_macos_universal\.dmg$`),
				repository("windows", "KaringX/clashmi", `(?i)_windows_x64\.exe$`),
				repository("linux", "KaringX/clashmi", `(?i)_linux_amd64\.AppImage$`, `(?i)_linux_amd64\.deb$`),
			},
		},
		{
			ID: "koalaclash", Name: "Koala Clash", Core: "Mihomo", Featured: true,
			Description: "Clash Verge Rev 的轻量增强分支。",
			Downloads: []DownloadDefinition{
				repository("windows", "coolcoala/koala-clash", `(?i)_x64-setup\.exe$`),
				repository("macos", "coolcoala/koala-clash", `(?i)_arm64\.pkg$`, `(?i)_x64\.pkg$`),
				repository("linux", "coolcoala/koala-clash", `(?i)_amd64\.deb$`),
			},
		},
		{
			ID: "flclashx", Name: "FlClashX", Core: "Mihomo", Featured: true,
			Description: "FlClash 的增强分支，支持 Android 与主流桌面系统。",
			Downloads: []DownloadDefinition{
				repository("android", "pluralplay/FlClashX", `(?i)android-universal\.apk$`),
				repository("windows", "pluralplay/FlClashX", `(?i)windows-amd64-setup\.exe$`),
				repository("macos", "pluralplay/FlClashX", `(?i)macos-arm64\.dmg$`, `(?i)macos-amd64\.dmg$`),
				repository("linux", "pluralplay/FlClashX", `(?i)linux-amd64\.AppImage$`, `(?i)linux-amd64\.deb$`),
			},
		},
		{
			ID: "rabbit-hole", Name: "Rabbit Hole", Core: "Mihomo", Featured: true,
			Description: "面向 Apple 平台的简洁客户端。",
			Downloads: []DownloadDefinition{
				external("ios", "app-store", "https://apps.apple.com/app/rabbithole-vpn-client/id6683309629"),
				external("macos", "app-store", "https://apps.apple.com/app/rabbithole-vpn-client/id6683309629"),
			},
		},
		{
			ID: "prizrakbox", Name: "Prizrak-Box", Core: "Mihomo",
			Description: "带有自定义路由模板的桌面客户端。",
			Downloads: []DownloadDefinition{
				repository("windows", "legiz-ru/Prizrak-Box", `(?i)windows-amd64-Setup\.exe$`, `(?i)windows-amd64\.msi$`),
				repository("macos", "legiz-ru/Prizrak-Box", `(?i)macos-arm64\.zip$`, `(?i)macos-amd64\.zip$`),
				repository("linux", "legiz-ru/Prizrak-Box", `(?i)linux-amd64\.deb$`),
			},
		},
		{
			ID: "flowvy", Name: "Flowvy", Core: "Mihomo", Featured: true,
			Description: "操作简洁的桌面 Mihomo 客户端。",
			Downloads: []DownloadDefinition{
				repository("windows", "flowvy-proxy/desktop", `(?i)_x64\.exe$`),
				repository("macos", "flowvy-proxy/desktop", `(?i)_arm64\.dmg$`),
				repository("linux", "flowvy-proxy/desktop", `(?i)_x64\.deb$`),
			},
		},
		{
			ID: "throne", Name: "Throne", Core: "Sing-box",
			Description: "功能丰富的桌面 Sing-box 客户端；HWID 需要在客户端中启用。",
			Downloads: []DownloadDefinition{
				repository("windows", "throneproj/Throne", `(?i)-windows-universal-installer\.exe$`),
				repository("macos", "throneproj/Throne", `(?i)-macos-arm64\.zip$`, `(?i)-macos-amd64\.zip$`),
				repository("linux", "throneproj/Throne", `(?i)-debian-amd64\.deb$`),
			},
		},
		{
			ID: "v2raytun", Name: "V2rayTun", Core: "Xray",
			Description: "轻量的多平台 Xray 客户端。",
			Downloads: []DownloadDefinition{
				external("android", "google-play", "https://play.google.com/store/apps/details?id=com.v2raytun.android"),
				external("ios", "app-store", "https://apps.apple.com/en/app/v2raytun/id6476628951"),
				external("macos", "app-store", "https://apps.apple.com/en/app/v2raytun/id6476628951"),
				external("windows", "website", "https://v2raytun.com/"),
			},
		},
		{
			ID: "shadowrocket", Name: "ShadowRocket", Core: "Other",
			Description: "Apple 平台常用的付费代理客户端；HWID 需要在客户端中启用。",
			Downloads: []DownloadDefinition{
				external("ios", "app-store", "https://apps.apple.com/us/app/shadowrocket/id932747118"),
				external("macos", "app-store", "https://apps.apple.com/us/app/shadowrocket/id932747118"),
			},
		},
		{
			ID: "incy", Name: "INCY", Core: "Xray",
			Description: "支持多种协议的现代多平台客户端。",
			Downloads: []DownloadDefinition{
				repositoryWithFallback("android", "INCY-DEV/incy-platforms", "https://play.google.com/store/apps/details?id=llc.itdev.incy", `(?i)^Incy\.apk$`),
				external("ios", "app-store", "https://apps.apple.com/us/app/incy/id6756943388"),
				external("macos", "app-store", "https://apps.apple.com/us/app/incy/id6756943388"),
				repository("windows", "INCY-DEV/incy-platforms", `(?i)-windows-setup\.exe$`),
				repository("linux", "INCY-DEV/incy-platforms", `(?i)-linux-x64\.deb$`),
			},
		},
		{
			ID: "renoarx", Name: "RenoarX", Core: "Xray",
			Description: "面向 Windows 的现代 Xray 客户端。",
			Downloads: []DownloadDefinition{
				repository("windows", "RonnyFX/RenoarX", `(?i)-Setup-[^/]+\.exe$`),
			},
		},
		{
			ID: "deskbox", Name: "DeskBox", Core: "Sing-box",
			Description: "用于管理 Sing-box 的简洁桌面客户端。",
			Downloads: []DownloadDefinition{
				repository("windows", "mihail-jdanov/DeskBox", `(?i)_windows\.zip$`),
				repository("macos", "mihail-jdanov/DeskBox", `(?i)_macos_apple_silicon\.zip$`, `(?i)_macos_intel\.zip$`),
				repository("linux", "mihail-jdanov/DeskBox", `(?i)_ubuntu\.tar\.gz$`),
			},
		},
		{
			ID: "inhive", Name: "InHive", Core: "Sing-box",
			Description: "基于 Sing-box 的邀请制客户端。",
			Downloads: []DownloadDefinition{
				repository("android", "TwilgateLabs/inhive-android", `(?i)^InHive\.apk$`, `(?i)-arm64-v8a\.apk$`),
				repository("windows", "TwilgateLabs/inhive-windows", `(?i)-setup\.exe$`),
			},
		},
	}
}

func external(platform, source, address string) DownloadDefinition {
	return DownloadDefinition{Platform: platform, Source: source, URL: address}
}

func repository(platform, name string, patterns ...string) DownloadDefinition {
	return DownloadDefinition{Platform: platform, Source: "github", Repository: name, Patterns: patterns}
}

func repositoryWithFallback(platform, name, fallback string, patterns ...string) DownloadDefinition {
	definition := repository(platform, name, patterns...)
	definition.FallbackURL = fallback
	return definition
}
