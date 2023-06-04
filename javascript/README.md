## Javascript Usage

I wrote the Javascript version for the browser, and then I made some modifications so it should work unchanged with Node.js. However, I haven't tested it on Node.js, so if you do use it and it doesn't work, create an issue.

```html
<script src="tokenmonster.js"></script>
```
```javascript
const vocab = new TokenMonster();
vocab.load(vocab_URL);
// in the browser vocab_URL must be a URL, but in Node.js it can be either a URL or a local filepath

let tokens = vocab.tokenize(inputText);

const decoder = vocab.Decoder()
const tokenStrDecoded = decoder.detokenize(tokens);
```

The entirety of capcode.js is also included within the tokenmonster.js file. It uses only native libraries that are available in both browsers and Node.js, so it has no other dependencies. I even ended up writing a custom hashtable in there.