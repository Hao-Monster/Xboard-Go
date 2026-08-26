export type DistributorLocale = "zh-CN" | "en-US";

export const distributorCopy = {
  "zh-CN": {
    buy: "购买订阅", orders: "我的订单", invite: "我的邀请", knowledge: "使用文档", clients: "客户端下载", logout: "退出登录",
    language: "语言", dark: "深色模式", light: "浅色模式",
    centerTitle: "分销订阅中心", centerSubtitle: "每个订单生成一份独立订阅，客户扫码领取后不可再次领取。",
    allPlans: "全部套餐", highTraffic: "大流量", unlimitedSpeed: "不限速", unlimitedDevices: "不限设备", featured: "精选套餐",
    traffic: "套餐流量", speed: "速度限制", devices: "同时在线", resetMethod: "流量重置", period: "周期", planDetails: "套餐详情",
    followSystem: "跟随系统", firstDayMonth: "每月 1 日", monthlyReset: "按月重置", neverReset: "不重置", firstDayYear: "每年 1 月 1 日", yearlyReset: "按年重置",
    monthly: "月付", quarterly: "季付", halfYearly: "半年付", yearly: "年付", twoYearly: "两年付", threeYearly: "三年付", onetime: "一次性", resetTraffic: "流量重置包",
    deliveryStepOne: "选择套餐并下单", deliveryStepTwo: "客户扫描二维码", deliveryStepThree: "确认节点可用",
    loadingPlans: "正在加载套餐…", noPlans: "当前没有符合条件的可售套餐。", independentDelivery: "独立订阅交付，客户扫码即可添加。",
    orderAction: "已确认，直接下单", ordering: "正在下单…", deliveryTitle: "订阅交付", done: "已添加成功",
    deliveryHint: "请让客户扫描二维码添加订阅。关闭弹窗不会关闭或撤销订阅。", closeDelivery: "关闭订阅交付", customerQR: "客户订阅二维码",
    orderNo: "订单号", plan: "套餐", deviceBinding: "设备绑定", boundDevices: "已绑定设备", unboundDevice: "尚未绑定设备", hwidDisabled: "未启用设备绑定",
    copyImage: "复制图片", copiedImage: "二维码图片已复制", copyImageUnsupported: "当前浏览器无法复制图片，请下载后发送", downloadImage: "下载图片", buyAgain: "再次购买该套餐",
    ordersTitle: "我的订单", ordersSubtitle: "原始订单与续费订单按同一份客户订阅分组展示。", refresh: "刷新",
    searchOrders: "搜索订单", orderSearchPlaceholder: "订单号或客户名称", search: "搜索", clear: "清空", settlement: "结算状态", all: "全部", unsettled: "未结算", settled: "已结算",
    exportExcel: "导出 Excel", exporting: "正在导出…", loadingOrders: "正在加载订单…", noOrders: "没有符合条件的订单。", orderList: "分销订单列表",
    created: "创建时间", customerName: "客户名称", planPeriod: "套餐 / 周期", amount: "金额", remark: "备注", actions: "操作", previous: "上一页", next: "下一页",
    newPurchase: "新购", renewalOrder: "续费", originalOrder: "原始订单", reading: "读取中…", subscriptionQR: "订阅二维码", viewEntitlement: "查看权益", hideEntitlement: "隐藏权益", renew: "续费",
    entitlement: "当前订阅权益", totalTraffic: "总流量", usedTraffic: "已用流量", remainingTraffic: "剩余流量", expiresAt: "到期时间", speedLimit: "限速", deviceLimit: "设备限制", connectionStatus: "连接状态",
    notConnected: "尚未连接", unknownNode: "未知节点", permanent: "长期有效", unlimited: "不限", qrRenewHint: "续费不会改变此二维码、订阅凭据或已绑定设备。", deviceStatus: "设备状态", close: "关闭",
    renewTitle: "续费现有订阅", renewHint: "续费后订阅链接、二维码、UUID 和已绑定设备保持不变。", renewCurrentExpiry: "当前到期", renewPeriod: "续费周期", loadingRenewal: "正在加载续费周期…", cancel: "取消", renewing: "正在续费…", renewConfirm: "确认续费",
    knowledgeTitle: "使用文档", knowledgeSubtitle: "查看产品使用方法与常见问题。", knowledgeSearch: "搜索使用文档", noArticles: "暂无使用文档", loadingKnowledge: "正在加载使用文档…", read: "阅读", updatedAt: "最后更新", publicShare: "公开分享",
    clientsTitle: "客户端下载", clientsSubtitle: "仅收录支持 HWID 设备识别的客户端；安装地址由服务端校验。", secureLinks: "安全中转链接", platformFilter: "平台筛选", loadingClients: "正在加载客户端目录…", noClients: "该平台暂无支持 HWID 的客户端。", clientList: "客户端目录", recommended: "推荐", choosePlatform: "选择下载平台", directDownload: "直接下载", qrDownload: "扫码下载", cloudDownload: "网盘下载", tutorial: "使用教程", clientFootnote: "安装包来自各客户端官方发布渠道；GitHub 客户端按受控规则匹配最新 Release。", qrFallback: "未单独配置扫码链接时，二维码使用直接下载地址。", generatingQR: "正在生成下载二维码…", openDownload: "当前设备打开下载链接"
  },
  "en-US": {
    buy: "Buy Subscription", orders: "My Orders", invite: "My Invitations", knowledge: "Documentation", clients: "Client downloads", logout: "Sign out",
    language: "Language", dark: "Dark mode", light: "Light mode",
    centerTitle: "Distributor Center", centerSubtitle: "Each order creates an independent subscription that can be claimed once.",
    allPlans: "All plans", highTraffic: "High traffic", unlimitedSpeed: "Unlimited speed", unlimitedDevices: "Unlimited devices", featured: "Featured",
    traffic: "Traffic", speed: "Speed", devices: "Devices", resetMethod: "Traffic reset", period: "Period", planDetails: "Plan details",
    followSystem: "System default", firstDayMonth: "1st of each month", monthlyReset: "Monthly", neverReset: "Never", firstDayYear: "January 1st", yearlyReset: "Yearly",
    monthly: "Monthly", quarterly: "Quarterly", halfYearly: "Half-year", yearly: "Yearly", twoYearly: "Two-year", threeYearly: "Three-year", onetime: "One-time", resetTraffic: "Traffic reset",
    deliveryStepOne: "Choose and order", deliveryStepTwo: "Customer scans QR", deliveryStepThree: "Verify service",
    loadingPlans: "Loading plans…", noPlans: "No available plan matches this filter.", independentDelivery: "Independent subscription delivery. The customer can scan the QR code to add it.",
    orderAction: "Confirmed — place order", ordering: "Placing order…", deliveryTitle: "Subscription delivery", done: "Added successfully",
    deliveryHint: "Ask the customer to scan the QR code. Closing this window does not close or revoke the subscription.", closeDelivery: "Close subscription delivery", customerQR: "Customer subscription QR",
    orderNo: "Order", plan: "Plan", deviceBinding: "Device binding", boundDevices: "Bound devices", unboundDevice: "No device bound", hwidDisabled: "Device binding disabled",
    copyImage: "Copy image", copiedImage: "QR image copied", copyImageUnsupported: "This browser cannot copy images. Download the image instead.", downloadImage: "Download image", buyAgain: "Buy this plan again",
    ordersTitle: "My Orders", ordersSubtitle: "Original purchases and renewals are grouped under the same customer subscription.", refresh: "Refresh",
    searchOrders: "Search orders", orderSearchPlaceholder: "Search by order or customer name", search: "Search", clear: "Clear", settlement: "Settlement", all: "All", unsettled: "Unsettled", settled: "Settled",
    exportExcel: "Export Excel", exporting: "Exporting…", loadingOrders: "Loading orders…", noOrders: "No orders match the current filters.", orderList: "Distributor orders",
    created: "Created", customerName: "Customer name", planPeriod: "Plan / period", amount: "Amount", remark: "Remark", actions: "Actions", previous: "Previous", next: "Next",
    newPurchase: "New purchase", renewalOrder: "Renewal", originalOrder: "Original order", reading: "Loading…", subscriptionQR: "Subscription QR", viewEntitlement: "View entitlement", hideEntitlement: "Hide entitlement", renew: "Renew",
    entitlement: "Subscription entitlement", totalTraffic: "Total traffic", usedTraffic: "Used traffic", remainingTraffic: "Remaining traffic", expiresAt: "Expires at", speedLimit: "Speed limit", deviceLimit: "Device limit", connectionStatus: "Connection",
    notConnected: "Not connected", unknownNode: "Unknown node", permanent: "Never expires", unlimited: "Unlimited", qrRenewHint: "Renewal does not change this QR code, subscription credential, or bound devices.", deviceStatus: "Device status", close: "Close",
    renewTitle: "Renew subscription", renewHint: "The subscription URL, QR code, UUID, and bound devices will stay unchanged.", renewCurrentExpiry: "Current expiry", renewPeriod: "Renewal period", loadingRenewal: "Loading renewal periods…", cancel: "Cancel", renewing: "Renewing…", renewConfirm: "Confirm renewal",
    knowledgeTitle: "Documentation", knowledgeSubtitle: "Browse product guides and frequently asked questions.", knowledgeSearch: "Search documentation", noArticles: "No documentation available", loadingKnowledge: "Loading documentation…", read: "Read", updatedAt: "Last updated", publicShare: "Public share",
    clientsTitle: "Client downloads", clientsSubtitle: "Only clients with HWID device identification are listed; installation URLs are validated by the server.", secureLinks: "Secure redirect links", platformFilter: "Platform filter", loadingClients: "Loading client catalog…", noClients: "No HWID-compatible client is available for this platform.", clientList: "Client catalog", recommended: "Recommended", choosePlatform: "Choose download platform", directDownload: "Direct download", qrDownload: "Download QR", cloudDownload: "Cloud download", tutorial: "Guide", clientFootnote: "Installers come from official client release channels; GitHub releases are matched by controlled server rules.", qrFallback: "When no separate QR URL is configured, the QR code uses the direct download URL.", generatingQR: "Generating download QR…", openDownload: "Open download link on this device"
  }
} as const;

export type DistributorCopy = typeof distributorCopy[DistributorLocale];

export function distributorPeriodLabels(locale: DistributorLocale) {
  const copy = distributorCopy[locale];
  return {
    monthly: copy.monthly, quarterly: copy.quarterly, half_yearly: copy.halfYearly, yearly: copy.yearly,
    two_yearly: copy.twoYearly, three_yearly: copy.threeYearly, onetime: copy.onetime, reset_traffic: copy.resetTraffic
  } as const;
}

export function distributorCloseLabel(locale: DistributorLocale, title: string): string {
  return locale === "zh-CN" ? `关闭${title}` : `Close ${title}`;
}
