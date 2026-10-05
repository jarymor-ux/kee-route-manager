package setup

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

var (
	ErrCancelled    = errors.New("configuration wizard cancelled")
	ErrNotConfirmed = errors.New("configuration creation not confirmed")
)

const (
	updateRepository = "jarymor-ux/kee-route-manager"
	updatePublicKey  = "t8ZyoMK5zMz2vTBuWaH8HIwMOo+E1nJXydOak0RWKAE"
)

type Messages struct {
	PlatformTitle          string
	PlatformHelp           string
	XrayBinaryHelp         string
	XrayBinaryPrompt       string
	XrayConfigDirHelp      string
	XrayConfigDirPrompt    string
	BaseRoutingHelp        string
	BaseRoutingPrompt      string
	InboundTagsHelp        string
	InboundTagsPrompt      string
	OutboundTagHelp        string
	OutboundTagPrompt      string
	SubscriptionURLHelp    string
	SubscriptionURLPrompt  string
	SubscriptionNamePrompt string
	SubscriptionEnabled    string
	AddHeader              string
	HeaderName             string
	HeaderValue            string
	AddSubscription        string
	ScoreHelp              string
	ScorePrompt            string
	HealthHelp             string
	HealthPrompt           string
	AddHealth              string
	PoolPrompt             string
	SpeedEnable            string
	SpeedURLHelp           string
	SpeedURLPrompt         string
	UpdatesEnable          string
	UpdateChannel          string
	SummaryTitle           string
	SummaryPlatform        string
	SummarySubscriptions   string
	SummaryScoreTargets    string
	SummaryHealthTargets   string
	SummaryPool            string
	SummarySpeed           string
	SummaryUpdates         string
	SummaryChannel         string
	Enabled                string
	Disabled               string
	Create                 string
	Overwrite              string
	InvalidChoice          string
	InvalidURL             string
	Required               string
	InvalidBool            string
	InvalidPool            string
	InvalidSpeedTemplate   string
	InvalidChannel         string
	Written                string
}

var messagesRU = Messages{
	PlatformTitle:          "Платформа:",
	PlatformHelp:           "Выберите платформу, на которой будет работать Kee Route Manager.",
	XrayBinaryHelp:         "Путь к исполняемому файлу Xray.",
	XrayBinaryPrompt:       "Xray binary",
	XrayConfigDirHelp:      "Каталог конфигурации Xray, в который Kee Route Manager будет записывать управляемые файлы.",
	XrayConfigDirPrompt:    "Xray config directory",
	BaseRoutingHelp:        "Базовый routing-файл Xray, содержащий правило, которое будет заменяться управляемым outbound.",
	BaseRoutingPrompt:      "Base routing file",
	InboundTagsHelp:        "Inbound tags правила маршрутизации. Укажите через запятую.",
	InboundTagsPrompt:      "Inbound tags",
	OutboundTagHelp:        "Outbound tag правила, которое Kee Route Manager должен заменять. Если tag неочевиден, используйте: kee-route-managerctl route-candidates --file PATH. При неоднозначности tag автоматически не выбирается.",
	OutboundTagPrompt:      "Outbound tag",
	SubscriptionURLHelp:    "URL источника подписки с узлами.",
	SubscriptionURLPrompt:  "Subscription URL",
	SubscriptionNamePrompt: "Subscription name",
	SubscriptionEnabled:    "Enable subscription? [Y/n]",
	AddHeader:              "Add HTTP header? [y/N]",
	HeaderName:             "Header name",
	HeaderValue:            "Header value (не будет показан в summary)",
	AddSubscription:        "Add another subscription? [y/N]",
	ScoreHelp:              "Score target используется для сравнительной оценки доступных узлов.",
	ScorePrompt:            "Score target URL",
	HealthHelp:             "Health targets используются для проверки реальной доступности маршрута. Рекомендуются два независимых hostname.",
	HealthPrompt:           "Health target URL",
	AddHealth:              "Add another health target? [y/N]",
	PoolPrompt:             "Pool size [5]",
	SpeedEnable:            "Enable speed test? [y/N]",
	SpeedURLHelp:           "URL template для download benchmark. Обязателен placeholder {bytes}.",
	SpeedURLPrompt:         "Speed test URL template",
	UpdatesEnable:          "Enable updates? [y/N]",
	UpdateChannel:          "Update channel [rc]",
	SummaryTitle:           "Итог:",
	SummaryPlatform:        "Platform",
	SummarySubscriptions:   "Subscriptions",
	SummaryScoreTargets:    "Score targets",
	SummaryHealthTargets:   "Health targets",
	SummaryPool:            "Pool size",
	SummarySpeed:           "Speed test",
	SummaryUpdates:         "Updates",
	SummaryChannel:         "Channel",
	Enabled:                "enabled",
	Disabled:               "disabled",
	Create:                 "Создать конфигурацию? [Y/n]",
	Overwrite:              "Файл уже существует. Перезаписать? [y/N]",
	InvalidChoice:          "Неверный выбор.",
	InvalidURL:             "Введите корректный http(s) URL.",
	Required:               "Обязательное значение не может быть пустым.",
	InvalidBool:            "Введите y или n.",
	InvalidPool:            "Pool size должен быть числом от 1 до 20.",
	InvalidSpeedTemplate:   "URL template должен быть корректным http(s) URL и содержать {bytes}.",
	InvalidChannel:         "Channel должен быть rc или stable.",
	Written:                "Конфигурация записана:",
}

var messagesEN = Messages{
	PlatformTitle:          "Platform:",
	PlatformHelp:           "Choose the platform where Kee Route Manager will run.",
	XrayBinaryHelp:         "Path to the Xray executable.",
	XrayBinaryPrompt:       "Xray binary",
	XrayConfigDirHelp:      "Xray configuration directory where Kee Route Manager will write managed files.",
	XrayConfigDirPrompt:    "Xray config directory",
	BaseRoutingHelp:        "Base Xray routing file containing the rule whose outbound will be managed.",
	BaseRoutingPrompt:      "Base routing file",
	InboundTagsHelp:        "Inbound tags for the routing rule. Separate multiple tags with commas.",
	InboundTagsPrompt:      "Inbound tags",
	OutboundTagHelp:        "Outbound tag of the rule Kee Route Manager should replace. If it is unclear, use: kee-route-managerctl route-candidates --file PATH. Ambiguous routing tags are never selected automatically.",
	OutboundTagPrompt:      "Outbound tag",
	SubscriptionURLHelp:    "URL of a subscription source containing nodes.",
	SubscriptionURLPrompt:  "Subscription URL",
	SubscriptionNamePrompt: "Subscription name",
	SubscriptionEnabled:    "Enable subscription? [Y/n]",
	AddHeader:              "Add HTTP header? [y/N]",
	HeaderName:             "Header name",
	HeaderValue:            "Header value (not shown in summary)",
	AddSubscription:        "Add another subscription? [y/N]",
	ScoreHelp:              "The score target is used to compare the quality of available nodes.",
	ScorePrompt:            "Score target URL",
	HealthHelp:             "Health targets verify real route availability. Two independent hostnames are recommended.",
	HealthPrompt:           "Health target URL",
	AddHealth:              "Add another health target? [y/N]",
	PoolPrompt:             "Pool size [5]",
	SpeedEnable:            "Enable speed test? [y/N]",
	SpeedURLHelp:           "Download benchmark URL template. It must contain the {bytes} placeholder.",
	SpeedURLPrompt:         "Speed test URL template",
	UpdatesEnable:          "Enable updates? [y/N]",
	UpdateChannel:          "Update channel [rc]",
	SummaryTitle:           "Summary:",
	SummaryPlatform:        "Platform",
	SummarySubscriptions:   "Subscriptions",
	SummaryScoreTargets:    "Score targets",
	SummaryHealthTargets:   "Health targets",
	SummaryPool:            "Pool size",
	SummarySpeed:           "Speed test",
	SummaryUpdates:         "Updates",
	SummaryChannel:         "Channel",
	Enabled:                "enabled",
	Disabled:               "disabled",
	Create:                 "Create configuration? [Y/n]",
	Overwrite:              "Output file already exists. Overwrite? [y/N]",
	InvalidChoice:          "Invalid choice.",
	InvalidURL:             "Enter a valid http(s) URL.",
	Required:               "This value is required.",
	InvalidBool:            "Enter y or n.",
	InvalidPool:            "Pool size must be a number from 1 to 20.",
	InvalidSpeedTemplate:   "URL template must be a valid http(s) URL and contain {bytes}.",
	InvalidChannel:         "Channel must be rc or stable.",
	Written:                "Configuration written:",
}

type wizard struct {
	in  *bufio.Reader
	out io.Writer
	msg Messages
}

func InitConfig(in io.Reader, out io.Writer, outputPath string, overwrite bool) error {
	if in == nil || out == nil {
		return fmt.Errorf("wizard input and output are required")
	}
	if strings.TrimSpace(outputPath) == "" {
		return fmt.Errorf("output path is required")
	}

	w := &wizard{in: bufio.NewReader(in), out: out}
	if err := w.chooseLanguage(); err != nil {
		return err
	}
	opts, summary, err := w.collect()
	if err != nil {
		return err
	}
	cfg, err := BuildControllerConfig(opts)
	if err != nil {
		return err
	}
	w.printSummary(summary)
	ok, err := w.askBool(w.msg.Create, true)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotConfirmed
	}

	if !overwrite {
		if _, err := os.Stat(outputPath); err == nil {
			ok, err = w.askBool(w.msg.Overwrite, false)
			if err != nil {
				return err
			}
			if !ok {
				return ErrNotConfirmed
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("check output file: %w", err)
		}
	}

	if err := WriteConfig(outputPath, cfg); err != nil {
		return err
	}
	loaded, err := config.Load(outputPath)
	if err != nil {
		return fmt.Errorf("validate written config: %w", err)
	}
	if err := loaded.Validate(); err != nil {
		return fmt.Errorf("validate written config: %w", err)
	}
	fmt.Fprintf(w.out, "%s %s\n", w.msg.Written, outputPath)
	return nil
}

func (w *wizard) chooseLanguage() error {
	for {
		fmt.Fprintln(w.out, "Choose language / Выберите язык:")
		fmt.Fprintln(w.out)
		fmt.Fprintln(w.out, "1. Русский")
		fmt.Fprintln(w.out, "2. English")
		answer, err := w.read()
		if err != nil {
			return err
		}
		switch answer {
		case "1":
			w.msg = messagesRU
			return nil
		case "2":
			w.msg = messagesEN
			return nil
		default:
			fmt.Fprintln(w.out, "Invalid choice / Неверный выбор.")
		}
	}
}

type wizardSummary struct {
	platform      string
	subscriptions int
	scoreTargets  int
	healthTargets int
	poolSize      int
	speed         bool
	updates       bool
	channel       string
}

func (w *wizard) collect() (SetupOptions, wizardSummary, error) {
	platform, err := w.askPlatform()
	if err != nil {
		return SetupOptions{}, wizardSummary{}, err
	}
	binaryDefault, configDirDefault, routingDefault := platformDefaults(platform)

	fmt.Fprintln(w.out, w.msg.XrayBinaryHelp)
	binary, err := w.askDefault(w.msg.XrayBinaryPrompt, binaryDefault)
	if err != nil {
		return SetupOptions{}, wizardSummary{}, err
	}
	fmt.Fprintln(w.out, w.msg.XrayConfigDirHelp)
	configDir, err := w.askDefault(w.msg.XrayConfigDirPrompt, configDirDefault)
	if err != nil {
		return SetupOptions{}, wizardSummary{}, err
	}
	fmt.Fprintln(w.out, w.msg.BaseRoutingHelp)
	routing, err := w.askDefault(w.msg.BaseRoutingPrompt, routingDefault)
	if err != nil {
		return SetupOptions{}, wizardSummary{}, err
	}
	fmt.Fprintln(w.out, w.msg.InboundTagsHelp)
	inboundRaw, err := w.askDefault(w.msg.InboundTagsPrompt, "redirect,tproxy")
	if err != nil {
		return SetupOptions{}, wizardSummary{}, err
	}
	inbound := splitTags(inboundRaw)
	if len(inbound) == 0 {
		return SetupOptions{}, wizardSummary{}, fmt.Errorf("inbound tags are required")
	}
	fmt.Fprintln(w.out, w.msg.OutboundTagHelp)
	outbound, err := w.askRequired(w.msg.OutboundTagPrompt)
	if err != nil {
		return SetupOptions{}, wizardSummary{}, err
	}

	subscriptions, err := w.askSubscriptions()
	if err != nil {
		return SetupOptions{}, wizardSummary{}, err
	}

	fmt.Fprintln(w.out, w.msg.ScoreHelp)
	scoreURL, err := w.askURL(w.msg.ScorePrompt)
	if err != nil {
		return SetupOptions{}, wizardSummary{}, err
	}
	score := []config.Target{target("score1", "Score target", scoreURL)}

	fmt.Fprintln(w.out, w.msg.HealthHelp)
	health, err := w.askHealthTargets()
	if err != nil {
		return SetupOptions{}, wizardSummary{}, err
	}

	pool, err := w.askPool()
	if err != nil {
		return SetupOptions{}, wizardSummary{}, err
	}

	speedEnabled, err := w.askBool(w.msg.SpeedEnable, false)
	if err != nil {
		return SetupOptions{}, wizardSummary{}, err
	}
	speedURL := ""
	if speedEnabled {
		fmt.Fprintln(w.out, w.msg.SpeedURLHelp)
		speedURL, err = w.askSpeedURL()
		if err != nil {
			return SetupOptions{}, wizardSummary{}, err
		}
	}

	updatesEnabled, err := w.askBool(w.msg.UpdatesEnable, false)
	if err != nil {
		return SetupOptions{}, wizardSummary{}, err
	}
	channel := "rc"
	if updatesEnabled {
		channel, err = w.askChannel()
		if err != nil {
			return SetupOptions{}, wizardSummary{}, err
		}
	}

	opts := SetupOptions{
		Platform:      platform,
		Subscriptions: subscriptions,
		ScoreTargets:  score,
		HealthTargets: health,
		Xray: XrayOptions{
			Binary:              binary,
			ConfigDir:           configDir,
			ManagedDir:          configDir,
			BaseRoutingFile:     routing,
			InboundTags:         inbound,
			ReplaceOutboundTags: []string{outbound},
		},
		Pool: PoolOptions{Size: pool},
		Benchmark: BenchmarkOptions{
			SpeedEnabled:     speedEnabled,
			SpeedURLTemplate: speedURL,
		},
		Update: UpdateOptions{
			Enabled:          updatesEnabled,
			Channel:          channel,
			GitHubRepository: updateRepository,
			PublicKey:        updatePublicKey,
			AutoApply:        false,
		},
	}
	summary := wizardSummary{
		platform:      platformName(platform),
		subscriptions: len(subscriptions),
		scoreTargets:  len(score),
		healthTargets: len(health),
		poolSize:      pool,
		speed:         speedEnabled,
		updates:       updatesEnabled,
		channel:       channel,
	}
	return opts, summary, nil
}

func (w *wizard) askPlatform() (Platform, error) {
	for {
		fmt.Fprintln(w.out, w.msg.PlatformHelp)
		fmt.Fprintln(w.out, w.msg.PlatformTitle)
		fmt.Fprintln(w.out, "1. Keenetic")
		fmt.Fprintln(w.out, "2. OpenWrt")
		fmt.Fprintln(w.out, "3. Linux/systemd")
		answer, err := w.read()
		if err != nil {
			return "", err
		}
		switch answer {
		case "1":
			return PlatformKeenetic, nil
		case "2":
			return PlatformOpenWrt, nil
		case "3":
			return PlatformLinuxSystemd, nil
		default:
			fmt.Fprintln(w.out, w.msg.InvalidChoice)
		}
	}
}

func platformDefaults(p Platform) (string, string, string) {
	switch p {
	case PlatformKeenetic:
		return "/opt/sbin/xray", "/opt/etc/xray/configs", "/opt/etc/xray/configs/05_routing.json"
	default:
		return "/usr/bin/xray", "/etc/xray/configs", "/etc/xray/configs/05_routing.json"
	}
}

func platformName(p Platform) string {
	switch p {
	case PlatformKeenetic:
		return "Keenetic"
	case PlatformOpenWrt:
		return "OpenWrt"
	default:
		return "Linux/systemd"
	}
}

func (w *wizard) askSubscriptions() ([]config.Source, error) {
	var out []config.Source
	for {
		fmt.Fprintln(w.out, w.msg.SubscriptionURLHelp)
		u, err := w.askURL(w.msg.SubscriptionURLPrompt)
		if err != nil {
			return nil, err
		}
		name, err := w.askOptional(w.msg.SubscriptionNamePrompt)
		if err != nil {
			return nil, err
		}
		if name == "" {
			name = fmt.Sprintf("Subscription %d", len(out)+1)
		}
		enabled, err := w.askBool(w.msg.SubscriptionEnabled, true)
		if err != nil {
			return nil, err
		}
		headers := map[string]string{}
		for {
			add, err := w.askBool(w.msg.AddHeader, false)
			if err != nil {
				return nil, err
			}
			if !add {
				break
			}
			key, err := w.askRequired(w.msg.HeaderName)
			if err != nil {
				return nil, err
			}
			value, err := w.askRequired(w.msg.HeaderValue)
			if err != nil {
				return nil, err
			}
			headers[key] = value
		}
		out = append(out, config.Source{
			ID:      fmt.Sprintf("source%d", len(out)+1),
			Name:    name,
			URL:     u,
			Enabled: enabled,
			Headers: headers,
		})
		more, err := w.askBool(w.msg.AddSubscription, false)
		if err != nil {
			return nil, err
		}
		if !more {
			return out, nil
		}
	}
}

func (w *wizard) askHealthTargets() ([]config.Target, error) {
	var out []config.Target
	for {
		u, err := w.askURL(w.msg.HealthPrompt)
		if err != nil {
			return nil, err
		}
		out = append(out, target(fmt.Sprintf("health%d", len(out)+1), fmt.Sprintf("Health target %d", len(out)+1), u))
		more, err := w.askBool(w.msg.AddHealth, false)
		if err != nil {
			return nil, err
		}
		if !more {
			return out, nil
		}
	}
}

func target(id, name, u string) config.Target {
	return config.Target{
		ID:               id,
		Name:             name,
		URL:              u,
		Weight:           1,
		Policy:           "2xx3xx",
		MaxResponseBytes: 64 << 10,
	}
}

func (w *wizard) askPool() (int, error) {
	for {
		answer, err := w.prompt(w.msg.PoolPrompt)
		if err != nil {
			return 0, err
		}
		if answer == "" {
			return 5, nil
		}
		n, err := strconv.Atoi(answer)
		if err == nil && n >= 1 && n <= 20 {
			return n, nil
		}
		fmt.Fprintln(w.out, w.msg.InvalidPool)
	}
}

func (w *wizard) askSpeedURL() (string, error) {
	for {
		answer, err := w.prompt(w.msg.SpeedURLPrompt)
		if err != nil {
			return "", err
		}
		if validHTTPURL(answer) && strings.Contains(answer, "{bytes}") {
			return answer, nil
		}
		fmt.Fprintln(w.out, w.msg.InvalidSpeedTemplate)
	}
}

func (w *wizard) askChannel() (string, error) {
	for {
		answer, err := w.prompt(w.msg.UpdateChannel)
		if err != nil {
			return "", err
		}
		if answer == "" {
			return "rc", nil
		}
		if answer == "rc" || answer == "stable" {
			return answer, nil
		}
		fmt.Fprintln(w.out, w.msg.InvalidChannel)
	}
}

func (w *wizard) askURL(label string) (string, error) {
	for {
		answer, err := w.prompt(label)
		if err != nil {
			return "", err
		}
		if validHTTPURL(answer) {
			return answer, nil
		}
		fmt.Fprintln(w.out, w.msg.InvalidURL)
	}
}

func validHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https")
}

func (w *wizard) askRequired(label string) (string, error) {
	for {
		answer, err := w.prompt(label)
		if err != nil {
			return "", err
		}
		if answer != "" {
			return answer, nil
		}
		fmt.Fprintln(w.out, w.msg.Required)
	}
}

func (w *wizard) askOptional(label string) (string, error) {
	return w.prompt(label)
}

func (w *wizard) askDefault(label, def string) (string, error) {
	answer, err := w.prompt(fmt.Sprintf("%s [%s]", label, def))
	if err != nil {
		return "", err
	}
	if answer == "" {
		return def, nil
	}
	return answer, nil
}

func (w *wizard) askBool(label string, def bool) (bool, error) {
	for {
		answer, err := w.prompt(label)
		if err != nil {
			return false, err
		}
		switch strings.ToLower(answer) {
		case "":
			return def, nil
		case "y", "yes", "д", "да":
			return true, nil
		case "n", "no", "н", "нет":
			return false, nil
		default:
			fmt.Fprintln(w.out, w.msg.InvalidBool)
		}
	}
}

func (w *wizard) prompt(label string) (string, error) {
	fmt.Fprintf(w.out, "%s: ", label)
	return w.read()
}

func (w *wizard) read() (string, error) {
	line, err := w.in.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && len(line) > 0) {
		return "", err
	}
	answer := strings.TrimSpace(line)
	switch strings.ToLower(answer) {
	case "cancel", "отмена":
		return "", ErrCancelled
	}
	return answer, nil
}

func splitTags(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if tag := strings.TrimSpace(part); tag != "" {
			out = append(out, tag)
		}
	}
	return out
}

func (w *wizard) printSummary(s wizardSummary) {
	speed := w.msg.Disabled
	if s.speed {
		speed = w.msg.Enabled
	}
	updates := w.msg.Disabled
	if s.updates {
		updates = w.msg.Enabled
	}
	fmt.Fprintln(w.out)
	fmt.Fprintln(w.out, w.msg.SummaryTitle)
	fmt.Fprintf(w.out, "%s: %s\n", w.msg.SummaryPlatform, s.platform)
	fmt.Fprintf(w.out, "%s: %d\n", w.msg.SummarySubscriptions, s.subscriptions)
	fmt.Fprintf(w.out, "%s: %d\n", w.msg.SummaryScoreTargets, s.scoreTargets)
	fmt.Fprintf(w.out, "%s: %d\n", w.msg.SummaryHealthTargets, s.healthTargets)
	fmt.Fprintf(w.out, "%s: %d\n", w.msg.SummaryPool, s.poolSize)
	fmt.Fprintf(w.out, "%s: %s\n", w.msg.SummarySpeed, speed)
	fmt.Fprintf(w.out, "%s: %s\n", w.msg.SummaryUpdates, updates)
	fmt.Fprintf(w.out, "%s: %s\n", w.msg.SummaryChannel, s.channel)
}
