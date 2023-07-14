package leveldbbench

import (
	"context"
	"crypto/sha1"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/rs/zerolog/log"
	progressbar "github.com/schollz/progressbar/v3"
	"github.com/spf13/cobra"
	leveldb "github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/iterator"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/syndtr/goleveldb/leveldb/util"
	"math/rand"
	"os"
	"sync"
	"time"
)

var (
	//go:embed usage.md
	usage string
	// memory leak?
	knownKeys           map[string][]byte
	knownKeysMutex      sync.RWMutex
	randSrc             *rand.Rand
	randSrcMutex        sync.Mutex
	smallFillLimit      *uint64
	largeFillLimit      *uint64
	noWriteMerge        *bool
	syncWrites          *bool
	dontFillCache       *bool
	readStrict          *bool
	keySize             *uint64
	smallValueSize      *uint64
	largeValueSize      *uint64
	degreeOfParallelism *uint8
	readLimit           *uint64
)

type (
	LoadTestOperation int
	TestResult        struct {
		StartTime    time.Time
		EndTime      time.Time
		TestDuration time.Duration
		Description  string
		OpCount      uint64
		Stats        *leveldb.DBStats
		OpRate       float64
	}
)

func NewTestResult(startTime, endTime time.Time, desc string, opCount uint64, db *leveldb.DB) *TestResult {
	tr := new(TestResult)
	s := new(leveldb.DBStats)
	db.Stats(s)
	tr.Stats = s
	tr.StartTime = startTime
	tr.EndTime = endTime
	tr.TestDuration = endTime.Sub(startTime)
	tr.Description = desc
	tr.OpCount = opCount
	tr.OpRate = float64(opCount) / tr.TestDuration.Seconds()

	log.Info().Dur("testDuration", tr.TestDuration).Str("desc", tr.Description).Msg("recorded result")
	log.Debug().Interface("result", tr).Msg("recorded result")
	return tr
}

var LevelDBBenchCmd = &cobra.Command{
	Use:   "leveldbbench [flags]",
	Short: "Perform a level db benchmark",
	Long:  usage,
	RunE: func(cmd *cobra.Command, args []string) error {
		log.Info().Msg("Starting level db test")
		knownKeys = make(map[string][]byte, 0)
		db, err := leveldb.OpenFile("_benchmark_db", nil)
		if err != nil {
			return err
		}
		ctx := context.Background()
		wo := opt.WriteOptions{
			NoWriteMerge: *noWriteMerge,
			Sync:         *syncWrites,
		}
		ro := opt.ReadOptions{
			DontFillCache: *dontFillCache,
		}
		if *readStrict {
			ro.Strict = opt.StrictAll
		} else {
			ro.Strict = opt.DefaultStrict
		}
		var start time.Time
		trs := make([]*TestResult, 0)

		start = time.Now()
		writeData(ctx, db, &wo, *smallValueSize, 0, *smallFillLimit, true)
		trs = append(trs, NewTestResult(start, time.Now(), "small seq fill", *smallFillLimit, db))

		start = time.Now()
		writeData(ctx, db, &wo, *smallValueSize, 0, *smallFillLimit, true)
		trs = append(trs, NewTestResult(start, time.Now(), "small seq overwrite", *smallFillLimit, db))

		start = time.Now()
		writeData(ctx, db, &wo, *smallValueSize, 0, *smallFillLimit, false)
		trs = append(trs, NewTestResult(start, time.Now(), "small rand fill", *smallFillLimit, db))

		start = time.Now()
		writeData(ctx, db, &wo, *smallValueSize, 0, *smallFillLimit, false)
		trs = append(trs, NewTestResult(start, time.Now(), "small rand overwrite", *smallFillLimit, db))

		start = time.Now()
		writeData(ctx, db, &wo, *smallValueSize, 0, *smallFillLimit, false)
		trs = append(trs, NewTestResult(start, time.Now(), "small rand overwrite", *smallFillLimit, db))

		start = time.Now()
		writeData(ctx, db, &wo, *smallValueSize, 0, *smallFillLimit, false)
		trs = append(trs, NewTestResult(start, time.Now(), "small rand overwrite", *smallFillLimit, db))

		start = time.Now()
		readSeq(ctx, db, &wo, *readLimit)
		trs = append(trs, NewTestResult(start, time.Now(), "sequential read", *readLimit, db))

		start = time.Now()
		writeData(ctx, db, &wo, *largeValueSize, *smallFillLimit*2, *largeFillLimit, false)
		trs = append(trs, NewTestResult(start, time.Now(), "large rand fill", *largeFillLimit, db))

		start = time.Now()
		writeData(ctx, db, &wo, *largeValueSize, *smallFillLimit*2, *largeFillLimit, false)
		trs = append(trs, NewTestResult(start, time.Now(), "large rand overwrite", *largeFillLimit, db))

		start = time.Now()
		readSeq(ctx, db, &wo, *readLimit)
		trs = append(trs, NewTestResult(start, time.Now(), "sequential read", *readLimit, db))

		start = time.Now()
		readRandom(ctx, db, &ro, *readLimit)
		trs = append(trs, NewTestResult(start, time.Now(), "random read", *readLimit, db))

		start = time.Now()
		runFullCompact(ctx, db, &wo)
		trs = append(trs, NewTestResult(start, time.Now(), "compaction", 1, db))

		log.Info().Msg("Close DB")
		defer db.Close()

		jsonResults, err := json.Marshal(trs)
		if err != nil {
			return err
		}
		fmt.Println(string(jsonResults))
		return nil
	},
	Args: func(cmd *cobra.Command, args []string) error {
		return nil
	},
}

func runFullCompact(ctx context.Context, db *leveldb.DB, wo *opt.WriteOptions) {
	err := db.CompactRange(util.Range{nil, nil})
	if err != nil {
		log.Fatal().Err(err).Msg("error compacting data")
	}
}
func writeData(ctx context.Context, db *leveldb.DB, wo *opt.WriteOptions, valueSize, startIndex, writeLimit uint64, sequential bool) {
	var i uint64 = startIndex
	var wg sync.WaitGroup
	pool := make(chan bool, *degreeOfParallelism)
	bar := getNewProgessBar(int64(writeLimit), fmt.Sprintf("Write: %d", valueSize))
	defer bar.Finish()
	lim := writeLimit + startIndex
	for ; i < lim; i = i + 1 {
		pool <- true
		wg.Add(1)
		go func() {
			bar.Add(1)
			k, v := makeKV(i, valueSize, sequential)
			err := db.Put(k, v, wo)
			if err != nil {
				log.Fatal().Err(err).Msg("Failed to put value")
			}
			wg.Done()
			<-pool
		}()
	}
	wg.Wait()
}

func readSeq(ctx context.Context, db *leveldb.DB, wo *opt.WriteOptions, limit uint64) {
	pb := getNewProgessBar(int64(limit), "sequential reads")
	defer pb.Finish()
	var rCount uint64 = 0
	pool := make(chan bool, *degreeOfParallelism)
	var wg sync.WaitGroup
benchLoop:
	for {
		iter := db.NewIterator(nil, nil)
		for iter.Next() {
			rCount += 1
			pb.Add(1)
			pool <- true
			wg.Add(1)
			go func(i iterator.Iterator) {
				_ = i.Key()
				_ = i.Value()
				wg.Done()
				<-pool
			}(iter)

			if rCount >= limit {
				iter.Release()
				break benchLoop
			}
		}
		iter.Release()
		err := iter.Error()
		if err != nil {
			log.Fatal().Err(err).Msg("Error reading sequentially")
		}
	}
	wg.Wait()
}
func readRandom(ctx context.Context, db *leveldb.DB, ro *opt.ReadOptions, limit uint64) {
	pb := getNewProgessBar(int64(limit), "random reads")
	defer pb.Finish()
	var rCount uint64 = 0
	pool := make(chan bool, *degreeOfParallelism)
	var wg sync.WaitGroup

benchLoop:
	for {
		for _, randKey := range knownKeys {
			pool <- true
			wg.Add(1)
			go func() {
				rCount += 1
				pb.Add(1)

				db.Get(randKey, ro)
				wg.Done()
				<-pool
			}()
			if rCount >= limit {
				break benchLoop
			}
		}
	}
	wg.Wait()
}

func getNewProgessBar(max int64, description string) *progressbar.ProgressBar {
	pb := progressbar.NewOptions64(max,
		progressbar.OptionEnableColorCodes(false),
		progressbar.OptionSetDescription(description),
		progressbar.OptionSetElapsedTime(true),
		progressbar.OptionSetItsString("iop"),
		progressbar.OptionSetRenderBlankState(true),
		progressbar.OptionShowCount(),
		progressbar.OptionShowIts(),
		progressbar.OptionShowElapsedTimeOnFinish(),
		progressbar.OptionUseANSICodes(true),
		progressbar.OptionThrottle(1*time.Second),
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionOnCompletion(func() {
			fmt.Fprintln(os.Stderr)
		}),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "=",
			SaucerHead:    ">",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
		progressbar.OptionSetWidth(10),
		progressbar.OptionFullWidth(),
	)
	//return progressbar.Default(max, description)
	return pb
}

func makeKV(seed, valueSize uint64, sequential bool) ([]byte, []byte) {
	tmpKey := make([]byte, *keySize, *keySize)
	if sequential {
		// We're going to hack sequential by counting in reverse
		binary.BigEndian.PutUint64(tmpKey, math.MaxUint64-seed)
	} else {
		// For random (non-sequential) we'll just hash the number so it's still deterministic
		binary.LittleEndian.PutUint64(tmpKey, seed)
		hashedKey := sha1.Sum(tmpKey)
		tmpKey = hashedKey[0:*keySize]
	}
	knownKeysMutex.Lock()
	knownKeys[string(tmpKey)] = tmpKey
	knownKeysMutex.Unlock()

	log.Trace().Str("tmpKey", hex.EncodeToString(tmpKey)).Msg("Generated key")

	tmpValue := make([]byte, valueSize, valueSize)
	randSrcMutex.Lock()
	randSrc.Read(tmpValue)
	randSrcMutex.Unlock()
	return tmpKey, tmpValue
}

func init() {
	flagSet := LevelDBBenchCmd.PersistentFlags()
	smallFillLimit = flagSet.Uint64("small-fill-limit", 1000000, "The number of small entries to write in the db")
	largeFillLimit = flagSet.Uint64("large-fill-limit", 2000, "The number of large entries to write in the db")
	readLimit = flagSet.Uint64("read-limit", 10000000, "the number of reads will attempt to complete in a given test")
	smallValueSize = flagSet.Uint64("small-value-size", 32, "the number of random bytes to store")
	largeValueSize = flagSet.Uint64("large-value-size", 102400, "the number of random bytes to store for large tests")
	dontFillCache = flag.Bool("dont-fill-read-cache", false, "if false, then random reads will be cached")
	readStrict = flag.Bool("read-strict", false, "if true the rand reads will be made in strict mode")
	keySize = flagSet.Uint64("key-size", 8, "The byte length of the keys that we'll use")
	degreeOfParallelism = flagSet.Uint8("degree-of-parallelism", 1, "The number of concurrent iops we'll perform")
	noWriteMerge = flagSet.Bool("no-merge-write", false, "allows disabling write merge")
	syncWrites = flagSet.Bool("sync-writes", false, "sync each write")

	randSrc = rand.New(rand.NewSource(1))
}
