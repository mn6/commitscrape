[Screenshot of commitscrape in action](https://i.imgur.com/YTwFI82.png)

# commitscrape

GitHub calendars as a service, built in Go. Scrapes a GitHub profile for the pretty commit calendar, converts it from an SVG to classed divs, caches it to Redis, and serves it up in a JSON response.

## Setup

Install Redis and Go. Ensure Redis is running.

1. `go get` this repository.
2. Edit `config.toml` with your preferred settings.
3. `go build` and run the binary.

## API
* `GET /` : Retrieves GitHub calendar.

#### Possible query strings:
* `?columns=<column count>` : Only returns specified column count. Must be a number between 0 and 53.

## Example front-end setup

### JavaScript
```
fetch('<COMMITSCRAPE URL>/?columns=<OPTIONAL AMOUNT OF COLUMNS>')
  .then(response => {
    return response.text()
  })
  .then(res => {
    document.querySelector('#commits').innerHTML = res
  })
```

### CSS
```
.commitscrape-0 { background-color: 'some color for no commits' }
.commitscrape-1 { background-color: 'some color for light commits' }
.commitscrape-2 { background-color: 'some color for medium commits' }
.commitscrape-3 { background-color: 'some color for a lot of commits' }
```

## Live Examples
[My Website](https://mat.dog) 