package main

import "container/list"
import "flag"
import "fmt"
import "github.com/laochailan/notmuch-go"
import "io/ioutil"
import "log"
import "os"
import "os/user"
import "path"
import "path/filepath"

// Current limitations: A maildir cannot be named {'cur','new','tmp'}
// Only one account can be handled
// TODO: cache mailboxes list
// TODO: Add a config file

type cfg struct {
	nmDB     string
	newtag   string
	dryrun   bool
	loglevel int
	sync     string
}

var conf cfg = cfg{"", "new", true, 1, "mbsync -a"}

func parseArgs() {
	usr, _ := user.Current()
	conf.nmDB = usr.HomeDir + "/.mail"
	const (
		vUsage = "Verbosity level (0->2)"
		vDry   = "Dry run"
		vDB    = "Path to notmuch database"
		vNew   = "new tag (to remove) from notmuch"
		vHelp  = "Show this message"
		vSync  = "Sync command"
		s      = " (shorthand)"
	)

	flag.IntVar(&conf.loglevel, "verbose", conf.loglevel, vUsage)
	flag.BoolVar(&conf.dryrun, "dry-run", conf.dryrun, vDry)
	flag.StringVar(&conf.nmDB, "notmuch-database", conf.nmDB, vDB)
	flag.StringVar(&conf.newtag, "newtag", conf.newtag, vNew)
	flag.StringVar(&conf.sync, "sync", conf.sync, vSync)
	help := flag.Bool("help", false, vHelp)

	flag.IntVar(&conf.loglevel, "v", conf.loglevel, vUsage+s)
	flag.StringVar(&conf.newtag, "n", conf.newtag, vNew+s)
	flag.StringVar(&conf.nmDB, "d", conf.nmDB, vDB+s)
	flag.StringVar(&conf.sync, "s", conf.sync, vSync+s)
	help2 := flag.Bool("h", false, vHelp+s)

	flag.Parse()
	if *help || *help2 {
		Usage()
	}
}

const (
	LIST           = 1 << 0
	ADD_TAGS       = 1 << 1
	DEL_TAGS       = 1 << 2
	DEL_NEW        = 1 << 3
	COPY_CHANGED   = 1 << 4
	RM_CHANGED     = 1 << 5
	NOTMUCH_UPDATE = 1 << 6
	MAILS_SYNC     = 1 << 7

	PRE_CMDS  = COPY_CHANGED | RM_CHANGED
	POST_CMDS = ADD_TAGS | DEL_TAGS | DEL_NEW
)

var commands = []struct {
	name  string
	help  string
	flags int
}{
	{"all", "Run everything but list", ^LIST},
	{"list", "List labels (exclusive with everything else)", LIST},
	{"pre", "Update mail files from tags (equivalent to copy+rm)", PRE_CMDS},
	{"post", "Update tags from mail files (equivalent to add+del+deln)", POST_CMDS},
	{"add", "Add missing tags", ADD_TAGS},
	{"del", "Remove tags from displaced/removed mails", DEL_TAGS},
	{"deln", "Remove any 'new' tags", DEL_NEW},
	{"copy", "Copy mails with new tags", COPY_CHANGED},
	{"rm", "Remove mails missing tags from directory", RM_CHANGED},
	{"notmuch", "Launch 'notmuch new'", NOTMUCH_UPDATE},
	{"sync", "Launc sync command", MAILS_SYNC},
}

func Usage() {
	fmt.Fprintf(os.Stderr,
		"Usage: %s [--help] [flags] command [...command]\n\n",
		os.Args[0])
	fmt.Fprintf(os.Stderr, "Available commands:\n")
	for _, c := range commands {
		fmt.Fprintf(os.Stderr, "\t* %s: %s\n", c.name, c.help)
	}
	fmt.Fprintf(os.Stderr, "Available flags:\n")
	flag.VisitAll(func(f *flag.Flag) {
		fmt.Fprintf(os.Stderr, "\t-%s\n\t\t%s (default: %s)\n",
			f.Name, f.Usage, f.DefValue)
	})
	os.Exit(1)
}

func getCmdFlags(cmd string) int {
	for _, c := range commands {
		if cmd == c.name {
			return c.flags
		}
	}
	fmt.Fprintf(os.Stderr, "Unknown command: '%s'\n", cmd)
	Usage()
	return 0
}

func prnt(lvl int, format string, a ...interface{}) {
	if lvl <= conf.loglevel {
		fmt.Printf(format, a...)
		fmt.Printf("\n")
	}
}

func isMbox(mailbase, mbox string) bool {
	name := path.Join(mailbase, mbox)
	for _, i := range []string{"cur", "new", "tmp"} {
		if s, e := os.Stat(path.Join(name, i)); e != nil || !s.IsDir() {
			return false
		}
	}
	return true
}

func getMboxesRec(mailbase, dir string, lst *list.List) {
	files, err := ioutil.ReadDir(path.Join(mailbase, dir))
	if err != nil {
		log.Fatal(err)
	}
	for _, f := range files {
		name := f.Name()
		if name == "cur" || name == "new" || name == "tmp" {
			continue
		}
		if !f.IsDir() {
			continue
		}
		fullname := path.Join(dir, name)
		getMboxesRec(mailbase, fullname, lst)
		if !isMbox(mailbase, fullname) {
			continue
		}
		lst.PushBack(fullname)
	}
}

func getMboxes(mailbase string) []string {
	l := list.New()
	getMboxesRec(mailbase, "./", l)
	if l.Len() == 0 {
		return nil
	}
	arr := make([]string, l.Len())
	i := 0
	for e := l.Front(); e != nil; e = e.Next() {
		arr[i] = e.Value.(string)
		i++
	}
	return arr
}

func addTags(maildir, basedir string, db *notmuch.Database, dry bool) {
	query := fmt.Sprintf("folder:\"%[1]s\" NOT tag:\"%[1]s\"", maildir)
	newquery := db.CreateQuery(query)
	if newquery == nil {
		log.Fatalf("Could not create query '%s'\n", query)
	}
	defer newquery.Destroy()

	if count := newquery.CountMessages(); count == 0 {
		prnt(1, "No mails to add to %s", maildir)
		return
	} else {
		prnt(1, "%d mails to tag to %s", count, maildir)
	}

	for msg := newquery.SearchMessages(); msg.Valid(); msg.MoveToNext() {
		curmsg := msg.Get()
		rel, err := filepath.Rel(basedir, curmsg.GetFileName())
		if err != nil {
			fmt.Fprintln(os.Stderr, "Mail at an unexpected location")
		}
		msgPos := path.Clean(fmt.Sprintf("%s/../..", rel))
		prnt(2, fmt.Sprintf("Tagging '%s' as '%s'.",
			curmsg.GetHeader("Subject"), msgPos))
		if !dry {
			curmsg.AddTag(msgPos)
		}
		curmsg.Destroy()
	}
}

func delTags(maildir, basedir string, db *notmuch.Database, dry bool) {
	query := fmt.Sprintf("tag:\"%[1]s\" NOT folder:\"%[1]s\"", maildir)
	newquery := db.CreateQuery(query)
	if newquery == nil {
		log.Fatalf("Could not create query '%s'\n", query)
	}
	defer newquery.Destroy()

	if count := newquery.CountMessages(); count == 0 {
		prnt(1, "No mails to untag from %s", maildir)
		return
	} else {
		prnt(1, string(count), " mails to untag from ", maildir)
	}

	for msg := newquery.SearchMessages(); msg.Valid(); msg.MoveToNext() {
		curmsg := msg.Get()
		prnt(2, "removing tag '%s' from '%s'.", maildir,
			curmsg.GetHeader("Subject"))
		if !dry {
			curmsg.RemoveTag(maildir)
		}
		curmsg.Destroy()
	}
}

func doPreCmds(cmds int, mboxes []string) {
	if (cmds & COPY_CHANGED) == COPY_CHANGED {
		fmt.Println("Copy changed not implemented yet")
		// XXX: TODO
	}
	if (cmds & RM_CHANGED) == RM_CHANGED {
		fmt.Println("Rm changed not implemented yet")
		// XXX: TODO
	}
}

func doPostCmds(cmds int, mboxes []string) {
	db, _ := notmuch.OpenDatabase(conf.nmDB,
		notmuch.DATABASE_MODE_READ_WRITE)
	if db == nil {
		fmt.Fprintln(os.Stderr, "Failed to load database at ",
			conf.nmDB)
		os.Exit(1)
	}
	defer db.Close()

	if (cmds & (ADD_TAGS | DEL_TAGS)) != 0 {
		for _, m := range mboxes {
			if (cmds & ADD_TAGS) == ADD_TAGS {
				addTags(m, conf.nmDB, db, conf.dryrun)
			}
			if (cmds & DEL_TAGS) == DEL_TAGS {
				delTags(m, conf.nmDB, db, conf.dryrun)
			}
		}
	}
	if (cmds & DEL_NEW) == DEL_NEW {
		delTags(conf.newtag, conf.nmDB, db, conf.dryrun)
	}
}

func main() {
	parseArgs()
	args := flag.Args()
	if len(args) == 0 {
		Usage()
	}
	cmds := 0
	for _, a := range args {
		cmds |= getCmdFlags(a)
	}

	mboxes := getMboxes(conf.nmDB)
	if (cmds & LIST) == LIST {
		for _, m := range mboxes {
			fmt.Println(m)
		}
		os.Exit(0)
	}

	if (cmds & PRE_CMDS) != 0 {
		doPreCmds(cmds, mboxes)
	}

	if (cmds & MAILS_SYNC) == MAILS_SYNC {
		fmt.Println("mail sync not implemented yet")
		// XXX: TODO
	}
	if (cmds & NOTMUCH_UPDATE) == NOTMUCH_UPDATE {
		fmt.Println("Notmuch update not implemented yet")
		// XXX: TODO
	}

	if (cmds & POST_CMDS) != 0 {
		doPostCmds(cmds, mboxes)
	}
}