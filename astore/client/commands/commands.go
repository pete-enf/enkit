package commands

import (
	"fmt"
	"github.com/enfabrica/enkit/astore/client/astore"
	arpc "github.com/enfabrica/enkit/astore/rpc/astore"
	"github.com/enfabrica/enkit/lib/client"
	"github.com/enfabrica/enkit/lib/client/commands"
	"github.com/enfabrica/enkit/lib/config"
	"github.com/enfabrica/enkit/lib/config/defcon"
	"github.com/enfabrica/enkit/lib/kflags/kcobra"
	"github.com/enfabrica/enkit/lib/oauth/cookie"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"math/rand"
	"os"
	"runtime"
	"strings"
)

type Root struct {
	*cobra.Command
	*commands.Base

	store client.ServerFlags
}

func New(rng *rand.Rand, base *commands.Base) *Root {
	root := NewRoot(base)

	root.AddCommand(NewDownload(root).Command)
	root.AddCommand(NewUpload(root).Command)
	root.AddCommand(NewList(root).Command)
	root.AddCommand(NewGuess(root).Command)
	root.AddCommand(NewTag(root).Command)
	root.AddCommand(NewNote(root).Command)
	root.AddCommand(NewPublic(root).Command)

	return root
}

func NewRoot(base *commands.Base) *Root {
	rc := &Root{
		Command: &cobra.Command{
			Use:           "astore",
			Short:         "Push, pull, and publish build artifacts",
			SilenceUsage:  true,
			SilenceErrors: true,
			Example: `  $ astore login carlo@enfabrica.net
        To obtain credentials to store/retrieve artifacts.
  
  $ astore upload build.out
        To upload a file in the artifact repository.
  
  $ astore upload build.out@experiments/builds/
        Same as above, stores the file in experiments/build.
  
  $ astore download experiments/builds/build.out
        Downloads the latest version of build.out.
  
  $ astore --help
        To have a nice help screen.`,
			Long: `astore - uploads and downloads artifacts`,
		},
		Base: base,
	}

	rc.store.Register(rc.PersistentFlags(), "store", "Artifacts store metadata server", "")
	return rc
}

func (rc *Root) StoreClient() (*astore.Client, error) {
	ids, err := rc.IdentityStore()
	if err != nil {
		return nil, err
	}

	// FIXME: make identity configurable.
	_, token, err := ids.Load("")
	if err != nil {
		return nil, fmt.Errorf("Please run:\n\n\tastore login user@domain.com\n\nTo retrieve the credentials necessary to perform the operation.\nFor debugging, this is the problem: %w", err)
	}

	// FIXME: make prefix configurable.
	storeconn, err := rc.store.Connect(client.WithCookie(cookie.CredentialsCookie("", token)))
	if err != nil {
		return nil, err
	}

	return astore.New(storeconn), nil
}

func (rc *Root) Formatter(mods ...Modifier) astore.Formatter {
	return NewTableFormatter(mods...)
}
func (rc *Root) OutputArtifacts(arts []*arpc.Artifact) {
	formatter := rc.Formatter(WithNoNesting)
	for _, art := range arts {
		formatter.Artifact(art)
	}
	formatter.Flush()
}

func (rc *Root) ConfigStore(namespace ...string) (config.Store, error) {
	return defcon.Open("astore", namespace...)
}

type Download struct {
	*cobra.Command
	root *Root

	ForceUid  bool
	ForcePath bool
	Output    string
	Overwrite bool
	Arch      string
}

func SystemArch() string {
	return strings.ToLower(runtime.GOARCH + "-" + runtime.GOOS)
}

func NewDownload(root *Root) *Download {
	command := &Download{
		Command: &cobra.Command{
			Use:     "download",
			Short:   "Downloads an artifact",
			Aliases: []string{"down", "get", "pull", "fetch"},
		},
		root: root,
	}
	command.Command.RunE = command.Run

	command.Flags().BoolVarP(&command.ForceUid, "force-uid", "u", false, "The argument specified identifies an uid")
	command.Flags().BoolVarP(&command.ForcePath, "force-path", "p", false, "The argument specified identifies a file path")
	command.Flags().StringVarP(&command.Output, "output", "o", ".", "Where to output the downloaded files. If multiple files are supplied, it must be a directory")
	command.Flags().BoolVarP(&command.Overwrite, "overwrite", "w", false, "Overwrite files that already exist")
	command.Flags().StringVarP(&command.Arch, "arch", "a", SystemArch(), "Architecture to download the file for")

	return command
}
func (dc *Download) Run(cmd *cobra.Command, args []string) error {
	if len(args) < 1 {
		return kcobra.NewUsageError(fmt.Errorf("use as 'astore download <path|uid>...' - one or more paths to download"))
	}
	if dc.ForceUid && dc.ForcePath {
		return kcobra.NewUsageError(fmt.Errorf("cannot specify --force-uid together with --force-path - an argument can be either one, but not both"))
	}

	mode := astore.IdAuto
	if dc.ForceUid {
		mode = astore.IdUid
	}
	if dc.ForcePath {
		mode = astore.IdPath
	}

	var archs []string
	switch strings.TrimSpace(dc.Arch) {
	case "":
		fallthrough
	case "all":
		archs = []string{"all"}
	default:
		archs = []string{dc.Arch, "all"}
	}

	options := astore.DownloadOptions{
		CommonOptions: dc.root.Options(),
		Options: &astore.Options{
			Formatter: dc.root.Formatter(WithNoNesting),
		},
		Output:       dc.Output,
		Overwrite:    dc.Overwrite,
		Architecture: archs,
	}

	client, err := dc.root.StoreClient()
	if err != nil {
		return err
	}

	err = client.Download(args, mode, options)
	if err != nil && os.IsExist(err) {
		return fmt.Errorf("file already exists? To overwrite, pass the -w or --overwrite flag - %s", err)
	}
	return err
}

type List struct {
	*cobra.Command
	root *Root

	Tag []string
	All bool
}

func NewList(root *Root) *List {
	command := &List{
		Command: &cobra.Command{
			Use:     "list",
			Short:   "Shows artifacts",
			Aliases: []string{"list", "show", "ls", "find"},
		},
		root: root,
	}
	command.Command.RunE = command.Run
	command.Flags().StringArrayVarP(&command.Tag, "tag", "t", []string{"latest"}, "Restrict the output to artifacts having this tag")
	command.Flags().BoolVarP(&command.All, "all", "l", false, "Show all binaries")

	return command
}

func (l *List) Run(cmd *cobra.Command, args []string) error {
	if len(args) > 1 {
		return kcobra.NewUsageError(fmt.Errorf("use as 'astore list [PATH]' - with a single, optional, PATH argument (got %d arguments)", len(args)))
	}
	query := ""
	if len(args) == 1 {
		query = args[0]
	}

	client, err := l.root.StoreClient()
	if err != nil {
		return err
	}

	tags := l.Tag
	if l.All {
		tags = []string{}
	}
	options := astore.ListOptions{
		CommonOptions: l.root.Options(),
		Tag:           tags,
	}

	arts, els, err := client.List(query, options)
	if err != nil {
		return err
	}

	formatter := l.root.Formatter()
	for _, art := range arts {
		formatter.Artifact(art)
	}
	if !l.All && len(arts) >= 1 {
		fmt.Printf("(only showing artifacts with %d tags: %v - use --all or -l to show all)\n", len(l.Tag), l.Tag)
	}

	for _, el := range els {
		formatter.Element(el)
	}
	formatter.Flush()

	return nil
}

type SuggestFlags astore.SuggestOptions

func (sf *SuggestFlags) Register(flagset *pflag.FlagSet) {
	flagset.StringVarP(&sf.Directory, "directory", "d", "", "Remote directory where to upload each file. If not specified explicitly, a path will be guessed using other heuristics")
	flagset.StringVarP(&sf.File, "file", "f", "", "Remote file name where to store all files. This is useful when uploading multiple files of different architectures")
	flagset.BoolVarP(&sf.DisableGit, "disable-git", "G", false, "Don't use the git repository to name the remote file")
	flagset.BoolVarP(&sf.DisableAt, "disable-at", "A", false, "Don't use the @ convention to name the remote file")
	flagset.BoolVarP(&sf.AllowAbsolute, "allow-absolute", "b", false, "Allow absolute local paths to name remote paths")
	flagset.BoolVarP(&sf.AllowSingleElement, "allow-single", "l", false, "Allow a asingle element path to be used as remote")
}

func (sf *SuggestFlags) Options() *astore.SuggestOptions {
	return (*astore.SuggestOptions)(sf)
}
