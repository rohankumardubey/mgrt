package internal

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/andrewpillar/mgrt/v3"
)

var SyncCmd = &Command{
	Usage: "sync <-type type> <-dsn dsn>",
	Short: "sync the performed revisions",
	Long: `Sync will update the local revisions with what has been performed in the
database. Doing this will overwrite any pre-existing revisions you may have
locally. The database to connect to is specified via the -type and -dsn flags,
or via the -db flag if a database connection has been configured via the "mgrt db"
command.

The -type flag specifies the type of database to connect to, it will be one of,

    mysql
    postgresql
    sqlite3

The -dsn flag specifies the data source name for the database. This will vary
depending on the type of database you're connecting to.

mysql and postgresql both allow for the URI connection string, such as,

    type://[user[:password]@][host]:[port][,...][/dbname][?param1=value1&...]

where type would either be mysql or postgresql. The postgresql type also allows
for the DSN string such as,

    host=localhost port=5432 dbname=mydb connect_timeout=10

sqlite3 however will accept a filepath, or the :memory: string, for example,

    -dsn :memory:`,
	Run: syncCmd,
}

func syncCmd(cmd *Command, args []string) {
	argv0 := args[0]

	var (
		typ    string
		dsn    string
		dbname string
	)

	fs := flag.NewFlagSet(cmd.Argv0+" "+argv0, flag.ExitOnError)
	fs.StringVar(&typ, "type", "", "the database type one of postgresql, sqlite3")
	fs.StringVar(&dsn, "dsn", "", "the dsn for the database to run the revisions against")
	fs.StringVar(&dbname, "db", "", "the database to connect to")
	fs.Parse(args[1:])

	if dbname != "" {
		it, err := getdbitem(dbname)

		if err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "%s %s: database %s does not exist\n", cmd.Argv0, argv0, dbname)
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "%s %s: %s\n", cmd.Argv0, argv0, err)
			os.Exit(1)
		}

		typ = it.Type
		dsn = it.DSN
	}

	if typ == "" {
		fmt.Fprintf(os.Stderr, "%s %s: database not specified\n", cmd.Argv0, argv0)
		os.Exit(1)
	}

	if dsn == "" {
		fmt.Fprintf(os.Stderr, "%s %s: database not specified\n", cmd.Argv0, argv0)
		os.Exit(1)
	}

	db, err := mgrt.Open(typ, dsn)

	if err != nil {
		fmt.Fprintf(os.Stderr, "%s %s: %s\n", cmd.Argv0, argv0, err)
		os.Exit(1)
	}

	defer db.Close()

	if err := os.MkdirAll(revisionsDir, os.FileMode(0755)); err != nil {
		fmt.Fprintf(os.Stderr, "%s %s: %s\n", cmd.Argv0, argv0, err)
		os.Exit(1)
	}

	revs, err := mgrt.GetRevisions(db, -1)

	if err != nil {
		fmt.Fprintf(os.Stderr, "%s %s: failed to get revisions: %s\n", cmd.Argv0, argv0, err)
		os.Exit(1)
	}

	for _, rev := range revs {
		dir := filepath.Join(revisionsDir, rev.Category)

		err = func() error {
			if err := os.MkdirAll(dir, os.FileMode(0755)); err != nil {
				return err
			}

			f, err := os.OpenFile(filepath.Join(dir, rev.ID+".sql"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(0644))

			if err != nil {
				return err
			}

			defer f.Close()

			f.WriteString(rev.String())
			return nil
		}()

		if err != nil {
			fmt.Fprintf(os.Stderr, "%s %s: failed to sync revisions: %s\n", cmd.Argv0, argv0, err)
			os.Exit(1)
		}
	}
}
