package main

import (
    "flag"
    "fmt"
    "log"
    "net/http"
    "net/url"
    "os"
    "os/signal"
    "regexp"
    "strings"
    "sync/atomic"
    "syscall"
    "time"

    colly "github.com/gocolly/colly/v2"
    digest "github.com/icholy/digest"
)

type Params struct {
    MaxPages     uint64
    MaxDepth     int
    MaxRedirects int
    Threads      int
    Delay        int64
}

func (params Params) String() string {
    return fmt.Sprintf("max %d pages, max depth %d, max %d redirects, %d threads, random delay %dms\n", params.MaxPages, params.MaxDepth, params.MaxRedirects, params.Threads, params.Delay)
}

type Link struct {
    url       string
    redirects int
}

func main() {

    var total uint64 = 1
    var params Params

    // replace sequences of space-like symbols to one space and trim
    spaceRegex := regexp.MustCompile(`\s+`)
    TrimSpace := func(s string) string {
        return strings.TrimSpace(spaceRegex.ReplaceAllString(s, " "))
    }

    flag.IntVar(&params.Threads, "threads", 5, "Maximum number of threads")
    flag.IntVar(&params.MaxDepth, "depth", 7, "Maximum crawling depth")
    flag.IntVar(&params.MaxRedirects, "redirects", 5, "Maximum number of recursive redirects")
    flag.Uint64Var(&params.MaxPages, "pages", 10000, "Max pages pages to check")
    flag.Int64Var(&params.Delay, "delay", 1000, "Random delay between requests, in milliseconds, default 1000 (1 sec)")

    flag.Parse()

    Site := flag.Arg(0)

    if Site == "" || len(flag.Args()) > 1 {
        fmt.Println("Usage: checker [options] https://domain.tld/")
        fmt.Println("Possible options:")
        flag.PrintDefaults()
        os.Exit(1)
    }

    defaultURL, err := url.Parse(Site)
    if err != nil {
        fmt.Println("Cannot parse url:", err)
        os.Exit(1)
    }

    if !defaultURL.IsAbs() {
        fmt.Println("Start url must be an absolute url (starting with https/http protocol)")
        os.Exit(1)
    }

    // create output file
    csv, err := NewCsvFile(defaultURL.Hostname() + ".csv")
    if err != nil {
        log.Fatal(err)
    }
    defer csv.Close()

    // close output file on SIGTERM
    closeSignalChannel := make(chan os.Signal)
    signal.Notify(closeSignalChannel, os.Interrupt, syscall.SIGTERM)
    go func() {
        <-closeSignalChannel
        csv.Close()
        os.Exit(0)
    }()

    // if start path not specified starting from root
    if defaultURL.Path == "" {
        defaultURL.Path = "/"
    }

    fmt.Printf("Starting crawling %s: %s", defaultURL.Redacted(), params)

    // output file header
    csv.Write([]string{
        "code",
        "url",
        "redirect",
        "source",
        "title",
        "h1",
        "description",
    })

    // Instantiate default Colly collector
    c := colly.NewCollector(
        colly.AllowedDomains(defaultURL.Hostname()),
        // ignoring image url
        colly.DisallowedURLFilters(
            regexp.MustCompile("\\.(jpg|jpeg|png|webp|gif|svg)$"),
        ),
    )

    c.MaxDepth = params.MaxDepth
    c.IgnoreRobotsTxt = false
    c.Async = params.Threads > 1

    // initial value of "random" delay between requests
    timeDelayStart := time.Duration(params.Delay) * time.Millisecond

    // increment of delay in case of http codes 429, 502, 503, and 504
    // decrement on successful request
    timeDelayIncDec := timeDelayStart / time.Duration(params.Threads)

    // maximum delay between request - 10 times more than delay increment
    timeDelayMax := 10 * time.Duration(params.Delay) * time.Millisecond

    // Initial value of limit rule
    LimitRule := colly.LimitRule{
        DomainGlob:  "*",
        Parallelism: params.Threads,
        RandomDelay: timeDelayStart,
    }

    c.Limit(&LimitRule)

    // set http digest auth
    if defaultURL.User.String() != "" {
        pwd, ok := defaultURL.User.Password()
        if !ok {
            fmt.Println("Must specify username:password in URL", err)
            os.Exit(1)
        }
        c.WithTransport(&digest.Transport{
            Username: defaultURL.User.Username(),
            Password: pwd,
        })
        defaultURL.User = nil
    }

    // don't process redirects by Colly
    c.SetRedirectHandler(func(req *http.Request, via []*http.Request) error {
        return http.ErrUseLastResponse
    })

    // adds links to request context
    AddURL := func(ctx *colly.Context, link Link) {
        links := ctx.GetAny("links")

        if links == nil {
            links = make([]Link, 0)
        }

        links = append(links.([]Link), link)
        ctx.Put("links", links)
    }

    // link processing
    VisitURL := func(r *colly.Request, link Link) {

        // checking for redirects or total pages limits
        if (link.redirects > params.MaxRedirects) || (total > params.MaxPages) {
            //fmt.Printf("Skip %s\n", link.url)
            return
        }

        // create new context for new link
        r.Ctx = colly.NewContext()
        r.Ctx.Put("source", r.URL.String())    // set current URL as link
        r.Ctx.Put("redirects", link.redirects) // set currect redirect count

        // increase total number of links on success
        if err := r.Visit(link.url); err == nil {
            //fmt.Printf("Visit %s from %s\n", link.url, r.URL.String())
            atomic.AddUint64(&total, 1)
        }

    }

    // Error handler called on network error and also on http "error", which is basically "not 200" http code code
    c.OnError(func(r *colly.Response, err error) {

        // looks like network error, do nothing
        if err != nil && r.StatusCode == 0 {
            fmt.Printf("Request #%d ERROR: %s\n", r.Request.ID, err)
            return
        }

        switch r.StatusCode {
        // processing redirects
        case 301, 302, 307, 308:
            location := r.Request.AbsoluteURL(r.Headers.Get("Location"))

            if location == "" {
                return
            }

            // store number of redirects in request context
            var redirects int
            ctxRedirects := r.Request.Ctx.GetAny("redirects")
            switch ctxRedirects.(type) {
            case int:
                redirects = ctxRedirects.(int)
            }

            // OnScraped will not be called so we "visit" it directly but increase redirect counter
            VisitURL(r.Request, Link{url: location, redirects: redirects + 1})

            fmt.Printf("Request #%d (%d) [%d] %s -> %s\n",
                r.Request.ID,
                r.Request.Depth,
                r.StatusCode,
                r.Request.URL.String(),
                location,
            )

            // write to output file
            csv.Write([]string{
                fmt.Sprintf("%d", r.StatusCode),
                r.Request.URL.String(),
                location,
                r.Ctx.Get("source"),
                "",
                "",
                "",
            })

        case 429, 502, 503, 504:
            // status codes which indicates bandwith limiting or backend overloading
            // try to adapt by increase a "random" delay but to a certain limit
            if LimitRule.RandomDelay < timeDelayMax {
                atomic.AddInt64((*int64)(&LimitRule.RandomDelay), int64(timeDelayIncDec)*2)
                fmt.Printf("HTTP %d, increasing delay by %d ms (%d)\n", r.StatusCode, timeDelayIncDec.Milliseconds()*2, LimitRule.RandomDelay.Milliseconds())
            } else {
                fmt.Printf("HTTP %d, not increasing delay as it is %d ms already\n", r.StatusCode, LimitRule.RandomDelay.Milliseconds())
            }

            r.Request.Retry()

        default:
            // real http errorneus condition, e.g. 500, 401, 403, 404
            fmt.Printf("Request #%d (%d) [%d] %s\n",
                r.Request.ID,
                r.Request.Depth,
                r.StatusCode,
                r.Request.URL.String(),
            )

            // write to output file
            csv.Write([]string{
                fmt.Sprintf("%d", r.StatusCode),
                r.Request.URL.String(),
                "",
                r.Ctx.Get("source"),
                "",
                "",
                "",
            })
        }
    })

    // colly html processing rules
    // extract canonical
    c.OnHTML("head link[rel='canonical']", func(e *colly.HTMLElement) {
        e.Request.Ctx.Put("canonical", strings.TrimSpace(e.Attr("href")))
        //fmt.Println("CANONICAL [", e.Request.ID, "]", e.Request.Ctx.Get("canonical"))
    })

    // extract page title
    c.OnHTML("head title", func(e *colly.HTMLElement) {
        e.Request.Ctx.Put("title", strings.TrimSpace(e.Text))
        //fmt.Println("TITLE [", e.Request.ID, "]", e.Request.Ctx)
    })

    // extract first H1
    c.OnHTML("body H1:first-of-type", func(e *colly.HTMLElement) {
        e.Request.Ctx.Put("h1", strings.TrimSpace(e.Text))
        //fmt.Println("H1 [", e.Request.ID, "]", e.Request.Ctx)
    })

    // extract page description
    c.OnHTML("head meta[name='description']", func(e *colly.HTMLElement) {
        e.Request.Ctx.Put("description", strings.TrimSpace(e.Attr("content")))
        //fmt.Println("META [", e.Request.ID, "]", e.Request.Ctx)
    })

    // links processing - have to be the last rule processing as they called in order
    c.OnHTML("a[href]", func(e *colly.HTMLElement) {
        // skip rel=nofollow links
        if strings.ToLower(e.Attr("rel")) == "nofollow" {
            return
        }
        // put links to request context to process later in OnScraped handler
        AddURL(e.Request.Ctx, Link{url: strings.TrimSpace(e.Attr("href"))})
    })

    // called on successful page download
    c.OnScraped(func(r *colly.Response) {
        // should decrease a "random" dealy if request finished successfuly (http code < 400)
        if r.StatusCode < 400 && LimitRule.RandomDelay > timeDelayStart {
            atomic.AddInt64((*int64)(&LimitRule.RandomDelay), int64(-timeDelayIncDec))
            fmt.Printf("STATUS %d, Decreasing delay by %d ms (%d)\n", r.StatusCode, timeDelayIncDec.Milliseconds(), LimitRule.RandomDelay.Milliseconds())
        }

        //fmt.Println("OnScraped [", r.Request.ID, "]", r.Request.Ctx)

        url := r.Request.URL.String()
        canonical := r.Request.Ctx.Get("canonical")

        // skip pages with non-empty canonical but not equal to current url
        if canonical != "" && url != canonical {

            fmt.Printf("Skip #%d (%d) [%d] %s CANONICAL: %s\n",
                r.Request.ID,
                r.Request.Depth,
                r.StatusCode,
                url,
                canonical,
            )

            // write to output file
            csv.Write([]string{
                fmt.Sprintf("%d", 310), // non-existent http code 310 in output csv in case we need to analyse them later
                url,
                canonical,
                r.Ctx.Get("source"),
                "",
                "",
                "",
            })

            // b
            AddURL(r.Request.Ctx, Link{url: canonical})
            return
        }

        fmt.Printf("Request #%d (%d) [%d] %s\n",
            r.Request.ID,
            r.Request.Depth,
            r.StatusCode,
            url,
        )

        // write to output file
        csv.Write([]string{
            fmt.Sprintf("%d", r.StatusCode),
            url,
            "", // redirect
            r.Ctx.Get("source"),
            TrimSpace(r.Ctx.Get("title")),
            TrimSpace(r.Ctx.Get("h1")),
            TrimSpace(r.Ctx.Get("description")),
        })

        // process all extracted in current request links
        links := r.Ctx.GetAny("links")
        if links != nil {
            for _, link := range links.([]Link) {
                VisitURL(r.Request, link)
            }
        }
    })

    // Start scraping
    c.Visit(defaultURL.String())

    // Wait until threads are finished
    c.Wait()
}
