package cmdadapter

import "strings"

var rootSensitiveCommands = map[string]string{
	"sudo":            "escalation",
	"su":              "escalation",
	"doas":            "escalation",
	"apt":             "package-manager",
	"apt-get":         "package-manager",
	"dnf":             "package-manager",
	"yum":             "package-manager",
	"apk":             "package-manager",
	"pacman":          "package-manager",
	"brew":            "package-manager",
	"mount":           "mount",
	"umount":          "mount",
	"iptables":        "network-mutation",
	"nft":             "network-mutation",
	"ip":              "network-mutation",
	"route":           "network-mutation",
	"pfctl":           "network-mutation",
	"resolvectl":      "resolver",
	"systemd-resolve": "resolver",
	"systemctl":       "service-manager",
	"service":         "service-manager",
	"launchctl":       "service-manager",
	"sysctl":          "system-management",
	"modprobe":        "system-management",
	"kmod":            "system-management",
}

func IsRootSensitiveCommand(command string) bool {
	_, ok := rootSensitiveCommands[strings.ToLower(command)]
	return ok
}

func RootSensitiveCategory(command string) string {
	return rootSensitiveCommands[strings.ToLower(command)]
}

const BuiltinRootSensitiveSource = `
function decideCommandAdapter(ctx) {
  var command = String(ctx.command.name || "");
  var category = "system-management";
  var packages = [];
  if (command === "sudo" && ctx.command.argv && ctx.command.argv.length > 1) {
    command = String(ctx.command.argv[1]);
  }
  if (command === "sudo" || command === "su" || command === "doas") category = "escalation";
  if (["apt", "apt-get", "dnf", "yum", "apk", "pacman", "brew"].indexOf(command) >= 0) category = "package-manager";
  if (["mount", "umount"].indexOf(command) >= 0) category = "mount";
  if (["iptables", "nft", "ip", "route", "pfctl"].indexOf(command) >= 0) category = "network-mutation";
  if (["resolvectl", "systemd-resolve"].indexOf(command) >= 0) category = "resolver";
  if (["systemctl", "service", "launchctl"].indexOf(command) >= 0) category = "service-manager";
  if (["sysctl", "modprobe", "kmod"].indexOf(command) >= 0) category = "system-management";
  if (category === "package-manager" && ctx.command.argv) {
    for (var i = 0; i < ctx.command.argv.length; i++) {
      var arg = String(ctx.command.argv[i]);
      if (arg !== "sudo" && arg !== command && arg !== "install" && arg.charAt(0) !== "-") packages.push(arg);
    }
  }
  var intent = {
    category: category,
    command: String(ctx.command.name || ""),
    classification: category,
    separationStatus: String((ctx.session && ctx.session.separationStatus) || "unknown"),
    nonClaim: String((ctx.session && ctx.session.nonClaim) || "Hideout does not claim guest-root containment for this run.")
  };
  if (packages.length > 0) {
    intent.operation = "install";
    intent.packages = packages;
    return {
      outcome: "proposeCapability",
      reason: "root-sensitive package-manager command captured as target intent; privileged setup is not applied by command adapters",
      capability: "guest.privilege.plan",
      intent: intent,
      suggestions: ["Use a base image with the package preinstalled", "Use 009 privilege separation before applying guest privilege plans"]
    };
  }
  return {
    outcome: "deny",
    reason: "root-sensitive command captured as target intent; Hideout does not claim absolute-path, syscall, setuid, or guest-root containment",
    exitCode: 126,
    stderr: "blocked by Hideout command adapter\n",
    intent: intent
  };
}
`
