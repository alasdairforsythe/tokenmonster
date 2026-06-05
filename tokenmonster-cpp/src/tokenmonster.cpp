#include <tokenmonster/tokenmonster.hpp>

#include <capcode/capcode.hpp>

#include <unicode/normalizer2.h>
#include <unicode/uchar.h>
#include <unicode/locid.h>
#include <unicode/unistr.h>

#include <algorithm>
#include <array>
#include <cstring>
#include <fstream>
#include <limits>
#include <string>
#include <unordered_map>

namespace tokenmonster {
namespace {

constexpr char32_t kRuneError = 0xFFFDU;
constexpr char32_t kMinHighSurrogate = 0xD800U;
constexpr char32_t kMaxHighSurrogate = 0xDBFFU;
constexpr char32_t kMinLowSurrogate = 0xDC00U;
constexpr char32_t kMaxLowSurrogate = 0xDFFFU;
using Bytes = std::vector<std::uint8_t>;

int min_int(int x, int y) { return x < y ? x : y; }
int max_int(int x, int y) { return x > y ? x : y; }
int max_zero_and(int x) { return x > 0 ? x : 0; }
int less_than(int x, int y) { return x < y ? 1 : 0; }
int equal_to(int x, int y) { return x == y ? 1 : 0; }

void append_bytes(Bytes& out, std::span<const std::uint8_t> bytes) {
  out.insert(out.end(), bytes.begin(), bytes.end());
}

struct Rune {
  char32_t value = kRuneError;
  int size = 0;
};

Rune decode_utf8(std::span<const std::uint8_t> bytes) {
  if (bytes.empty()) {
    return {kRuneError, 0};
  }
  const auto b0 = bytes[0];
  if (b0 < 0x80) {
    return {b0, 1};
  }
  auto invalid = Rune{kRuneError, 1};
  if (b0 < 0xC2) {
    return invalid;
  }
  if (b0 < 0xE0) {
    if (bytes.size() < 2 || (bytes[1] & 0xC0) != 0x80) return invalid;
    return {static_cast<char32_t>(((b0 & 0x1F) << 6) | (bytes[1] & 0x3F)), 2};
  }
  if (b0 < 0xF0) {
    if (bytes.size() < 3 || (bytes[1] & 0xC0) != 0x80 || (bytes[2] & 0xC0) != 0x80) {
      return invalid;
    }
    if (b0 == 0xE0 && bytes[1] < 0xA0) return invalid;
    if (b0 == 0xED && bytes[1] >= 0xA0) return invalid;
    return {static_cast<char32_t>(((b0 & 0x0F) << 12) | ((bytes[1] & 0x3F) << 6) |
                                  (bytes[2] & 0x3F)),
            3};
  }
  if (b0 < 0xF5) {
    if (bytes.size() < 4 || (bytes[1] & 0xC0) != 0x80 || (bytes[2] & 0xC0) != 0x80 ||
        (bytes[3] & 0xC0) != 0x80) {
      return invalid;
    }
    if (b0 == 0xF0 && bytes[1] < 0x90) return invalid;
    if (b0 == 0xF4 && bytes[1] >= 0x90) return invalid;
    return {static_cast<char32_t>(((b0 & 0x07) << 18) | ((bytes[1] & 0x3F) << 12) |
                                  ((bytes[2] & 0x3F) << 6) | (bytes[3] & 0x3F)),
            4};
  }
  return invalid;
}

int incomplete_utf8_bytes(std::span<const std::uint8_t> bytes) {
  const auto bytes_len = static_cast<int>(bytes.size());
  if (bytes_len == 0) return 0;
  if ((bytes[bytes_len - 1] & 0x80) == 0) return 0;
  int seq_start = bytes_len - 1;
  while (seq_start >= 0 && (bytes[seq_start] & 0xC0) == 0x80) {
    --seq_start;
  }
  if (seq_start == -1) return bytes_len;
  const auto first = bytes[seq_start];
  int seq_len = 0;
  if ((first & 0x80) == 0) {
    seq_len = 1;
  } else if ((first & 0xE0) == 0xC0) {
    seq_len = 2;
  } else if ((first & 0xF0) == 0xE0) {
    seq_len = 3;
  } else if ((first & 0xF8) == 0xF0) {
    seq_len = 4;
  } else {
    return bytes_len - seq_start;
  }
  if (bytes_len - seq_start < seq_len) {
    return seq_len - (bytes_len - seq_start);
  }
  if (seq_len == 1 && (bytes[seq_start] & 0xC0) != 0) return bytes_len;
  return 0;
}

int incomplete_utf16_bytes(std::span<const std::uint8_t> bytes) {
  const int bytes_len = static_cast<int>(bytes.size());
  if (bytes_len == 0) return 0;
  if (bytes_len % 2 != 0) {
    if (bytes_len >= 3) {
      auto last_three =
          static_cast<std::uint16_t>(bytes[bytes_len - 3] | (bytes[bytes_len - 2] << 8));
      if (last_three >= kMinHighSurrogate && last_three <= kMaxHighSurrogate) return 3;
    }
    return 1;
  }
  auto last_two =
      static_cast<std::uint16_t>(bytes[bytes_len - 2] | (bytes[bytes_len - 1] << 8));
  if (last_two >= kMinHighSurrogate && last_two <= kMaxHighSurrogate) return 2;
  auto first_two = static_cast<std::uint16_t>(bytes[0] | (bytes[1] << 8));
  if (first_two >= kMinLowSurrogate && first_two <= kMaxLowSurrogate) return 2;
  return 0;
}

class Reader {
 public:
  explicit Reader(const std::filesystem::path& path) {
    std::ifstream in(path, std::ios::binary);
    if (!in) throw Error("failed to open vocabulary file");
    data_ = Bytes(std::istreambuf_iterator<char>(in), std::istreambuf_iterator<char>());
  }

  std::uint8_t read_byte() {
    require(1);
    return data_[at_++];
  }

  std::uint32_t read_uint24() {
    require(3);
    std::uint32_t v = static_cast<std::uint32_t>(data_[at_]) |
                      (static_cast<std::uint32_t>(data_[at_ + 1]) << 8) |
                      (static_cast<std::uint32_t>(data_[at_ + 2]) << 16);
    at_ += 3;
    return v;
  }

  std::uint32_t read_uint32() {
    require(4);
    std::uint32_t v = static_cast<std::uint32_t>(data_[at_]) |
                      (static_cast<std::uint32_t>(data_[at_ + 1]) << 8) |
                      (static_cast<std::uint32_t>(data_[at_ + 2]) << 16) |
                      (static_cast<std::uint32_t>(data_[at_ + 3]) << 24);
    at_ += 4;
    return v;
  }

  float read_float32() {
    auto bits = read_uint32();
    float value = 0.0F;
    std::memcpy(&value, &bits, sizeof(value));
    return value;
  }

  Bytes read_bytes8() {
    auto n = read_byte();
    require(n);
    Bytes result(data_.begin() + static_cast<std::ptrdiff_t>(at_),
                 data_.begin() + static_cast<std::ptrdiff_t>(at_ + n));
    at_ += n;
    return result;
  }

  bool eof() const { return at_ == data_.size(); }

 private:
  void require(std::size_t n) const {
    if (at_ + n > data_.size()) throw Error("truncated vocabulary file");
  }

  Bytes data_;
  std::size_t at_ = 0;
};

Bytes nfd(Bytes input) {
  bool ascii = true;
  for (auto b : input) {
    if ((b & 0x80) != 0) {
      ascii = false;
      break;
    }
  }
  if (ascii) return input;

  UErrorCode status = U_ZERO_ERROR;
  auto* normalizer = icu::Normalizer2::getNFDInstance(status);
  if (U_FAILURE(status)) throw Error("normalization error");
  icu::UnicodeString u = icu::UnicodeString::fromUTF8(
      icu::StringPiece(reinterpret_cast<const char*>(input.data()), static_cast<int>(input.size())));
  icu::UnicodeString out;
  normalizer->normalize(u, out, status);
  if (U_FAILURE(status)) throw Error("normalization error");
  std::string s;
  out.toUTF8String(s);
  return Bytes(s.begin(), s.end());
}

Bytes lower_case(Bytes input) {
  bool has_non_ascii_or_upper = false;
  for (auto b : input) {
    if ((b & 0x80) != 0 || (b >= 'A' && b <= 'Z')) {
      has_non_ascii_or_upper = true;
      break;
    }
  }
  if (!has_non_ascii_or_upper) return input;

  icu::UnicodeString u = icu::UnicodeString::fromUTF8(
      icu::StringPiece(reinterpret_cast<const char*>(input.data()), static_cast<int>(input.size())));
  u.toLower(icu::Locale::getRoot());
  std::string s;
  u.toUTF8String(s);
  return Bytes(s.begin(), s.end());
}

Bytes remove_mn(Bytes input) {
  Bytes decomposed = nfd(std::move(input));
  Bytes out;
  for (std::size_t i = 0; i < decomposed.size();) {
    auto r = decode_utf8(std::span<const std::uint8_t>(decomposed).subspan(i));
    if (r.size <= 0) break;
    if (u_charType(static_cast<UChar32>(r.value)) != U_NON_SPACING_MARK) {
      append_bytes(out, std::span<const std::uint8_t>(decomposed).subspan(i, r.size));
    }
    i += static_cast<std::size_t>(r.size);
  }
  return out;
}

Bytes add_leading_space(Bytes b) {
  if (b.empty()) return b;
  if (b[0] == ' ') return b;
  b.resize(b.size() + 1);
  std::copy_backward(b.begin(), b.end() - 1, b.end());
  b[0] = ' ';
  return b;
}

Bytes trim_bytes(Bytes b) {
  int i = 0;
  for (; i < static_cast<int>(b.size()); ++i) {
    if (b[static_cast<std::size_t>(i)] > 32) break;
  }
  for (int i2 = static_cast<int>(b.size()) - 1; i2 >= 0; --i2) {
    if (b[static_cast<std::size_t>(i2)] > 32) {
      return Bytes(b.begin() + i, b.begin() + i2 + 1);
    }
  }
  return {};
}

Bytes trim_and_add_leading_space(Bytes b) {
  int i = 0;
  for (; i < static_cast<int>(b.size()); ++i) {
    if (b[static_cast<std::size_t>(i)] > 32) break;
  }
  int i2 = 0;
  for (i2 = static_cast<int>(b.size()) - 1; i2 >= 0; --i2) {
    if (b[static_cast<std::size_t>(i2)] > 32) break;
  }
  if (i == 0) {
    if (i2 < 0) return {};
    return add_leading_space(Bytes(b.begin(), b.begin() + i2));
  }
  if (i2 < 0) return {};
  b[static_cast<std::size_t>(i - 1)] = ' ';
  return Bytes(b.begin() + i - 1, b.begin() + i2 + 1);
}

Bytes collapse(Bytes input) {
  std::size_t on = 0;
  std::uint8_t last = 0;
  for (auto b : input) {
    if (b != 32) {
      input[on] = b;
      ++on;
    } else if (last != 32) {
      input[on] = 32;
      ++on;
    }
    last = b;
  }
  input.resize(on);
  return input;
}

Bytes unix_lines(Bytes input) {
  if (input.size() < 2) return input;
  std::size_t on = 0;
  for (std::size_t i = 0; i + 1 < input.size(); ++i) {
    auto b = input[i];
    if (b == '\r' && input[i + 1] == '\n') continue;
    input[on] = b;
    ++on;
  }
  input[on] = input.back();
  input.resize(on + 1);
  return input;
}

Bytes collapse_and_unix_lines(Bytes input) {
  std::size_t on = 0;
  std::uint8_t last = 0;
  for (auto b : input) {
    if (b != 32) {
      if (b != '\n') {
        input[on] = b;
        ++on;
      } else if (last == '\r') {
        input[on - 1] = '\n';
      } else {
        input[on] = b;
        ++on;
      }
    } else if (last != 32) {
      input[on] = 32;
      ++on;
    }
    last = b;
  }
  input.resize(on);
  return input;
}

Bytes quotemarks(Bytes input) {
  std::size_t on = 0;
  for (std::size_t i = 0; i < input.size(); ++i) {
    auto b = input[i];
    if (b != 152 && b != 153 && b != 156 && b != 157) {
      input[on] = b;
      ++on;
      continue;
    }
    if (i > 1 && input[i - 1] == 128 && input[i - 2] == 226) {
      input[on - 2] = b < 156 ? '\'' : '"';
      --on;
      continue;
    }
    input[on] = b;
    ++on;
  }
  input.resize(on);
  return input;
}

Bytes collapse_and_quotemarks(Bytes input) {
  std::size_t on = 0;
  std::uint8_t last = 0;
  for (std::size_t i = 0; i < input.size(); ++i) {
    auto b = input[i];
    if (b == 32) {
      if (last != 32) {
        input[on] = 32;
        ++on;
        last = 32;
      }
      continue;
    }
    last = b;
    if (b != 152 && b != 153 && b != 156 && b != 157) {
      input[on] = b;
      ++on;
      continue;
    }
    if (i > 1 && input[i - 1] == 128 && input[i - 2] == 226) {
      input[on - 2] = b < 156 ? '\'' : '"';
      --on;
      continue;
    }
    input[on] = b;
    ++on;
  }
  input.resize(on);
  return input;
}

Bytes collapse_quotemarks_unix_lines(Bytes input) {
  std::size_t on = 0;
  std::uint8_t last = 0;
  for (std::size_t i = 0; i < input.size(); ++i) {
    auto b = input[i];
    if (b == 32) {
      if (last != 32) {
        input[on] = 32;
        ++on;
        last = 32;
      }
      continue;
    }
    if (b == '\n' && last == '\r') {
      input[on - 1] = '\n';
      last = '\n';
      continue;
    }
    last = b;
    if (b != 152 && b != 153 && b != 156 && b != 157) {
      input[on] = b;
      ++on;
      continue;
    }
    if (i > 1 && input[i - 1] == 128 && input[i - 2] == 226) {
      input[on - 2] = b < 156 ? '\'' : '"';
      --on;
      continue;
    }
    input[on] = b;
    ++on;
  }
  input.resize(on);
  return input;
}

Bytes normalize_bytes(std::span<const std::uint8_t> data, std::uint8_t flag) {
  Bytes bytes(data.begin(), data.end());
  if (flag == 0) return bytes;
  if (flag == 1) return nfd(std::move(bytes));

  if ((flag & 128) != 0) {
    if ((flag & 16) != 0) {
      if ((flag & 8) != 0) {
        bytes = collapse_quotemarks_unix_lines(std::move(bytes));
        goto skipahead;
      }
      bytes = collapse_and_unix_lines(std::move(bytes));
      goto skipahead;
    }
    bytes = unix_lines(std::move(bytes));
  }
  if ((flag & 8) != 0) {
    if ((flag & 16) != 0) {
      bytes = collapse_and_quotemarks(std::move(bytes));
    } else {
      bytes = quotemarks(std::move(bytes));
    }
  } else if ((flag & 16) != 0) {
    bytes = collapse(std::move(bytes));
  }
skipahead:
  if ((flag & 32) != 0) {
    if ((flag & 64) != 0) {
      bytes = trim_and_add_leading_space(std::move(bytes));
    } else {
      bytes = trim_bytes(std::move(bytes));
    }
  } else if ((flag & 64) != 0) {
    bytes = add_leading_space(std::move(bytes));
  }

  if ((flag & 4) != 0) {
    bytes = remove_mn(std::move(bytes));
    if ((flag & 2) != 0) bytes = lower_case(std::move(bytes));
    return bytes;
  }
  if ((flag & 2) != 0) {
    if ((flag & 1) != 0) bytes = nfd(std::move(bytes));
    return lower_case(std::move(bytes));
  }
  if ((flag & 1) != 0) return nfd(std::move(bytes));
  return bytes;
}

Bytes apply_capcode(std::span<const std::uint8_t> data, std::uint8_t using_capcode) {
  if (using_capcode == 2) return capcode::encode(data);
  if (using_capcode == 1) return capcode::no_capcode_encode(data);
  return Bytes(data.begin(), data.end());
}

Bytes normalize_and_capcode(std::span<const std::uint8_t> data, std::uint8_t using_capcode,
                            std::uint8_t normalizer_flag) {
  auto normalized = normalize_bytes(data, normalizer_flag);
  return apply_capcode(normalized, using_capcode);
}

}  // namespace

class Vocab::FastDictionary {
 public:
  struct LongestResult {
    std::uint32_t index = 0;
    int length = 0;
    bool found = false;
  };

  void add(std::span<const std::uint8_t> key) {
    switch ((static_cast<int>(key.size()) - 1) / 8) {
      case 0: {
        auto [a, i] = bytes2uint64(key);
        if (i > 3) {
          std::uint64_t hash = a >> ((static_cast<std::uint64_t>(i) - 4) * 8);
          hash = (kFibonacci * hash) >> 40;
          bloom8_top4_[hash >> 6] |= std::uint64_t{1} << (hash & 63);
          if (i > 5) {
            hash = a >> ((static_cast<std::uint64_t>(i) - 6) * 8);
            hash = (kFibonacci * hash) >> 40;
            bloom8_top2_[hash >> 6] |= std::uint64_t{1} << (hash & 63);
          }
        }
        limit8_[i].push_back(a);
        order8_[i].push_back(total_);
        count_[i + 1]++;
        total_++;
        return;
      }
      case 1: {
        auto [a, unused] = bytes2uint64(key);
        (void)unused;
        auto [b, i] = bytes2uint64(key.subspan(8));
        std::uint64_t hash = ((kFnvOffset ^ a) * kFnvPrime) >> 40;
        bloom16_[hash >> 6] |= std::uint64_t{1} << (hash & 63);
        bloom_big_skip_[hash >> 6] |= std::uint64_t{1} << (hash & 63);
        if (i > 1) {
          hash = b >> ((static_cast<std::uint64_t>(i) - 2) * 8);
          hash = ((((kFnvOffset ^ (kFibonacci * hash)) * kFnvPrime) ^ a) * kFnvPrime) >> 40;
          bloom16_top6_[hash >> 6] |= std::uint64_t{1} << (hash & 63);
          if (i > 3) {
            hash = b >> ((static_cast<std::uint64_t>(i) - 4) * 8);
            hash = ((((kFnvOffset ^ (kFibonacci * hash)) * kFnvPrime) ^ a) * kFnvPrime) >> 40;
            bloom16_top4_[hash >> 6] |= std::uint64_t{1} << (hash & 63);
            if (i > 5) {
              hash = b >> ((static_cast<std::uint64_t>(i) - 6) * 8);
              hash =
                  ((((kFnvOffset ^ (kFibonacci * hash)) * kFnvPrime) ^ a) * kFnvPrime) >> 40;
              bloom16_top2_[hash >> 6] |= std::uint64_t{1} << (hash & 63);
            }
          }
        }
        limit16_[i].push_back({a, b});
        order16_[i].push_back(total_);
        count_[i + 9]++;
        total_++;
        return;
      }
      case 2: {
        auto [a, unused_a] = bytes2uint64(key);
        auto [b, unused_b] = bytes2uint64(key.subspan(8));
        (void)unused_a;
        (void)unused_b;
        auto [c, i] = bytes2uint64(key.subspan(16));
        std::uint64_t hash = ((((kFnvOffset ^ a) * kFnvPrime) ^ b) * kFnvPrime) >> 40;
        bloom24_[hash >> 6] |= std::uint64_t{1} << (hash & 63);
        hash = ((kFnvOffset ^ a) * kFnvPrime) >> 40;
        bloom_big_skip_[hash >> 6] |= std::uint64_t{1} << (hash & 63);
        limit24_[i].push_back({a, b, c});
        order24_[i].push_back(total_);
        count_[i + 17]++;
        total_++;
        return;
      }
      case 3: {
        auto [a, unused_a] = bytes2uint64(key);
        auto [b, unused_b] = bytes2uint64(key.subspan(8));
        auto [c, unused_c] = bytes2uint64(key.subspan(16));
        (void)unused_a;
        (void)unused_b;
        (void)unused_c;
        auto [d, i] = bytes2uint64(key.subspan(24));
        std::uint64_t hash = ((((kFnvOffset ^ a) * kFnvPrime) ^ b) * kFnvPrime) >> 40;
        bloom32_[hash >> 6] |= std::uint64_t{1} << (hash & 63);
        hash = ((kFnvOffset ^ a) * kFnvPrime) >> 40;
        bloom_big_skip_[hash >> 6] |= std::uint64_t{1} << (hash & 63);
        limit32_[i].push_back({a, b, c, d});
        order32_[i].push_back(total_);
        count_[i + 25]++;
        total_++;
        return;
      }
      case 4: {
        auto [a, unused_a] = bytes2uint64(key);
        auto [b, unused_b] = bytes2uint64(key.subspan(8));
        auto [c, unused_c] = bytes2uint64(key.subspan(16));
        auto [d, unused_d] = bytes2uint64(key.subspan(24));
        (void)unused_a;
        (void)unused_b;
        (void)unused_c;
        (void)unused_d;
        auto [e, i] = bytes2uint64(key.subspan(32));
        std::uint64_t hash = ((((kFnvOffset ^ a) * kFnvPrime) ^ b) * kFnvPrime) >> 40;
        bloom40_[hash >> 6] |= std::uint64_t{1} << (hash & 63);
        hash = ((kFnvOffset ^ a) * kFnvPrime) >> 40;
        bloom_big_skip_[hash >> 6] |= std::uint64_t{1} << (hash & 63);
        limit40_[i].push_back({a, b, c, d, e});
        order40_[i].push_back(total_);
        count_[i + 33]++;
        total_++;
        return;
      }
      default:
        throw Error("maximum key length is 40 bytes");
    }
  }

  void build() {
    int l = 0;
    int run = 0;
    std::uint32_t on = 0;

    map4_[0].clear();
    map4_[1].clear();
    for (auto& m : map8_) m.clear();
    for (auto& m : map16_) m.clear();
    map1_.fill(kFalse);
    map2_.fill(kFalse);

    {
      std::vector<KeyVal<std::uint64_t>> temp;
      for (run = 0; run < 8; run++) {
        if ((l = static_cast<int>(limit8_[run].size())) > 0) {
          auto& m = order8_[run];
          if (l != static_cast<int>(m.size())) throw Error("Build can only be run once");
          temp.resize(static_cast<std::size_t>(l));
          for (int z = 0; z < l; ++z) temp[z] = {m[z], limit8_[run][z]};
          m.clear();
          std::sort(temp.begin(), temp.end(), by_value<std::uint64_t>);
          auto& newkey = limit8_[run];
          switch (run) {
            case 0:
              for (int i = 0; i < l; ++i) {
                map1_[temp[i].value] = on;
                on++;
                newkey[i] = temp[i].value;
              }
              break;
            case 1:
              for (int i = 0; i < l; ++i) {
                map2_[temp[i].value] = on;
                on++;
                newkey[i] = temp[i].value;
              }
              break;
            case 2:
              for (int i = 0; i < l; ++i) {
                map4_[0][static_cast<std::uint32_t>(temp[i].value)] = on;
                on++;
                newkey[i] = temp[i].value;
              }
              break;
            case 3:
              for (int i = 0; i < l; ++i) {
                map4_[1][static_cast<std::uint32_t>(temp[i].value)] = on;
                on++;
                newkey[i] = temp[i].value;
              }
              break;
            default:
              for (int i = 0; i < l; ++i) {
                map8_[run - 4][temp[i].value] = on;
                on++;
                newkey[i] = temp[i].value;
              }
          }
        }
      }
    }

    {
      std::vector<KeyVal<Double>> temp;
      for (run = 0; run < 8; run++) {
        if ((l = static_cast<int>(limit16_[run].size())) > 0) {
          auto& m = order16_[run];
          if (l != static_cast<int>(m.size())) throw Error("Build can only be run once");
          temp.resize(static_cast<std::size_t>(l));
          for (int z = 0; z < l; ++z) temp[z] = {m[z], limit16_[run][z]};
          m.clear();
          std::sort(temp.begin(), temp.end(), by_value<Double>);
          auto& newkey = limit16_[run];
          for (int i = 0; i < l; ++i) {
            map16_[run][temp[i].value] = on;
            on++;
            newkey[i] = temp[i].value;
          }
        }
      }
    }

    sort_limit<3>(limit24_, order24_);
    sort_limit<4>(limit32_, order32_);
    sort_limit<5>(limit40_, order40_);

    for (run = 2; run < 41; run++) {
      count_[run] += count_[run - 1];
    }
  }

  std::pair<std::uint32_t, bool> find(std::span<const std::uint8_t> key) const {
    switch ((static_cast<int>(key.size()) - 1) / 8) {
      case 0:
        switch (key.size()) {
          case 0:
            return {0, false};
          case 1: {
            auto index = map1_[key[0]];
            if (index != kFalse) return {index, true};
            return {0, false};
          }
          case 2: {
            auto index = map2_[(static_cast<std::uint64_t>(key[0]) << 8) | key[1]];
            if (index != kFalse) return {index, true};
            return {0, false};
          }
          case 3: {
            auto it = map4_[0].find((static_cast<std::uint32_t>(key[0]) << 16) |
                                    (static_cast<std::uint32_t>(key[1]) << 8) | key[2]);
            return it == map4_[0].end() ? std::pair<std::uint32_t, bool>{0, false}
                                        : std::pair<std::uint32_t, bool>{it->second, true};
          }
          case 4: {
            auto it = map4_[1].find((static_cast<std::uint32_t>(key[0]) << 24) |
                                    (static_cast<std::uint32_t>(key[1]) << 16) |
                                    (static_cast<std::uint32_t>(key[2]) << 8) | key[3]);
            return it == map4_[1].end() ? std::pair<std::uint32_t, bool>{0, false}
                                        : std::pair<std::uint32_t, bool>{it->second, true};
          }
          default: {
            auto [a, l] = bytes2uint64(key);
            auto it = map8_[l - 4].find(a);
            return it == map8_[l - 4].end() ? std::pair<std::uint32_t, bool>{0, false}
                                            : std::pair<std::uint32_t, bool>{it->second, true};
          }
        }
      case 1: {
        auto [a, unused] = bytes2uint64(key);
        (void)unused;
        auto bit = ((kFnvOffset ^ a) * kFnvPrime) >> 40;
        if ((bloom16_[bit >> 6] & (std::uint64_t{1} << (bit & 63))) == 0) return {0, false};
        auto [b, l] = bytes2uint64(key.subspan(8));
        auto it = map16_[l].find({a, b});
        return it == map16_[l].end() ? std::pair<std::uint32_t, bool>{0, false}
                                     : std::pair<std::uint32_t, bool>{it->second, true};
      }
      case 2: {
        auto [a, unused_a] = bytes2uint64(key);
        auto [b, unused_b] = bytes2uint64(key.subspan(8));
        (void)unused_a;
        (void)unused_b;
        auto bit = ((((kFnvOffset ^ a) * kFnvPrime) ^ b) * kFnvPrime) >> 40;
        if ((bloom24_[bit >> 6] & (std::uint64_t{1} << (bit & 63))) == 0) return {0, false};
        auto [c, l] = bytes2uint64(key.subspan(16));
        return binary_find<3>(limit24_[l], {a, b, c}, count_[l + 16]);
      }
      case 3: {
        auto [a, unused_a] = bytes2uint64(key);
        auto [b, unused_b] = bytes2uint64(key.subspan(8));
        (void)unused_a;
        (void)unused_b;
        auto bit = ((((kFnvOffset ^ a) * kFnvPrime) ^ b) * kFnvPrime) >> 40;
        if ((bloom32_[bit >> 6] & (std::uint64_t{1} << (bit & 63))) == 0) return {0, false};
        auto [c, unused_c] = bytes2uint64(key.subspan(16));
        auto [d, l] = bytes2uint64(key.subspan(24));
        (void)unused_c;
        return binary_find<4>(limit32_[l], {a, b, c, d}, count_[l + 24]);
      }
      case 4: {
        auto [a, unused_a] = bytes2uint64(key);
        auto [b, unused_b] = bytes2uint64(key.subspan(8));
        (void)unused_a;
        (void)unused_b;
        auto bit = ((((kFnvOffset ^ a) * kFnvPrime) ^ b) * kFnvPrime) >> 40;
        if ((bloom40_[bit >> 6] & (std::uint64_t{1} << (bit & 63))) == 0) return {0, false};
        auto [c, unused_c] = bytes2uint64(key.subspan(16));
        auto [d, unused_d] = bytes2uint64(key.subspan(24));
        auto [e, l] = bytes2uint64(key.subspan(32));
        (void)unused_c;
        (void)unused_d;
        return binary_find<5>(limit40_[l], {a, b, c, d, e}, count_[l + 32]);
      }
      default:
        return {static_cast<std::uint32_t>(total_), false};
    }
  }

  LongestResult longest_substring(std::span<const std::uint8_t> key) const {
    std::uint64_t a = 0, b = 0, c = 0, d = 0, e = 0;
    int l = 0;
    switch ((static_cast<int>(key.size()) - 1) / 8) {
      case 0:
        if (!key.empty()) {
          std::tie(a, l) = bytes2uint64(key);
          return find0(key, a);
        }
        return {};
      case 1: {
        a = pack8(key);
        auto bit = ((kFnvOffset ^ a) * kFnvPrime) >> 40;
        if ((bloom16_[bit >> 6] & (std::uint64_t{1} << (bit & 63))) != 0) {
          std::tie(b, l) = bytes2uint64(key.subspan(8));
          if (auto result = find1({a, b}, l); result.found) return result;
        }
        return find0(key.first(8), a);
      }
      case 2: {
        a = pack8(key);
        auto hash = (kFnvOffset ^ a) * kFnvPrime;
        auto bit = hash >> 40;
        if ((bloom_big_skip_[bit >> 6] & (std::uint64_t{1} << (bit & 63))) == 0) {
          return find0(key.first(8), a);
        }
        b = pack8(key.subspan(8));
        auto bit2 = ((hash ^ b) * kFnvPrime) >> 40;
        if ((bloom24_[bit2 >> 6] & (std::uint64_t{1} << (bit2 & 63))) != 0) {
          std::tie(c, l) = bytes2uint64(key.subspan(16));
          if (auto result = find2(key, a, b, c, l); result.found) return result;
        }
        if ((bloom16_[bit >> 6] & (std::uint64_t{1} << (bit & 63))) != 0) {
          if (auto result = find1({a, b}, 7); result.found) return result;
        }
        return find0(key.first(8), a);
      }
      case 3: {
        a = pack8(key);
        auto hash = (kFnvOffset ^ a) * kFnvPrime;
        auto bit = hash >> 40;
        if ((bloom_big_skip_[bit >> 6] & (std::uint64_t{1} << (bit & 63))) == 0) {
          return find0(key.first(8), a);
        }
        c = pack8(key.subspan(16));
        b = pack8(key.subspan(8));
        auto bit2 = ((hash ^ b) * kFnvPrime) >> 40;
        auto mask = std::uint64_t{1} << (bit2 & 63);
        if ((bloom32_[bit2 >> 6] & mask) != 0) {
          std::tie(d, l) = bytes2uint64(key.subspan(24));
          if (auto result = find3(key, a, b, c, d, l); result.found) return result;
        }
        if ((bloom24_[bit2 >> 6] & mask) != 0) {
          if (auto result = find2(key.first(24), a, b, c, 7); result.found) return result;
        }
        if ((bloom16_[bit >> 6] & (std::uint64_t{1} << (bit & 63))) != 0) {
          if (auto result = find1({a, b}, 7); result.found) return result;
        }
        return find0(key.first(8), a);
      }
      case 4: {
        a = pack8(key);
        auto hash = (kFnvOffset ^ a) * kFnvPrime;
        auto bit = hash >> 40;
        if ((bloom_big_skip_[bit >> 6] & (std::uint64_t{1} << (bit & 63))) == 0) {
          return find0(key.first(8), a);
        }
        d = pack8(key.subspan(24));
        c = pack8(key.subspan(16));
        b = pack8(key.subspan(8));
        auto bit2 = ((hash ^ b) * kFnvPrime) >> 40;
        auto idx = bit2 >> 6;
        auto mask = std::uint64_t{1} << (bit2 & 63);
        if ((bloom40_[idx] & mask) != 0) {
          std::tie(e, l) = bytes2uint64(key.subspan(32));
          if (auto result = find4(key, a, b, c, d, e, l); result.found) return result;
        }
        if ((bloom32_[idx] & mask) != 0) {
          if (auto result = find3(key.first(32), a, b, c, d, 7); result.found) return result;
        }
        if ((bloom24_[idx] & mask) != 0) {
          if (auto result = find2(key.first(24), a, b, c, 7); result.found) return result;
        }
        if ((bloom16_[bit >> 6] & (std::uint64_t{1} << (bit & 63))) != 0) {
          if (auto result = find1({a, b}, 7); result.found) return result;
        }
        return find0(key.first(8), a);
      }
      default:
        return {};
    }
  }

  int longest_length() const {
    for (int run = 7; run >= 0; run--) {
      if (!limit40_[run].empty()) return 33 + run;
    }
    for (int run = 7; run >= 0; run--) {
      if (!limit32_[run].empty()) return 25 + run;
    }
    for (int run = 7; run >= 0; run--) {
      if (!limit24_[run].empty()) return 17 + run;
    }
    for (int run = 7; run >= 0; run--) {
      if (!limit16_[run].empty()) return 9 + run;
    }
    for (int run = 7; run >= 0; run--) {
      if (!limit8_[run].empty()) return 1 + run;
    }
    return 0;
  }

 private:
  using Double = std::array<std::uint64_t, 2>;

  template <typename T>
  struct KeyVal {
    int key = 0;
    T value{};
  };

  struct DoubleHash {
    std::size_t operator()(const Double& v) const noexcept {
      std::uint64_t h = (v[0] ^ (v[1] + 0x9e3779b97f4a7c15ULL + (v[0] << 6) + (v[0] >> 2)));
      return static_cast<std::size_t>(h);
    }
  };

  template <typename T>
  static bool by_value(const KeyVal<T>& a, const KeyVal<T>& b) {
    return a.value < b.value;
  }

  static std::pair<std::uint64_t, int> bytes2uint64(std::span<const std::uint8_t> word) {
    switch (word.size()) {
      case 0:
        return {0, 0};
      case 1:
        return {word[0], 0};
      case 2:
        return {(std::uint64_t(word[0]) << 8) | word[1], 1};
      case 3:
        return {(std::uint64_t(word[0]) << 16) | (std::uint64_t(word[1]) << 8) | word[2],
                2};
      case 4:
        return {(std::uint64_t(word[0]) << 24) | (std::uint64_t(word[1]) << 16) |
                    (std::uint64_t(word[2]) << 8) | word[3],
                3};
      case 5:
        return {(std::uint64_t(word[0]) << 32) | (std::uint64_t(word[1]) << 24) |
                    (std::uint64_t(word[2]) << 16) | (std::uint64_t(word[3]) << 8) | word[4],
                4};
      case 6:
        return {(std::uint64_t(word[0]) << 40) | (std::uint64_t(word[1]) << 32) |
                    (std::uint64_t(word[2]) << 24) | (std::uint64_t(word[3]) << 16) |
                    (std::uint64_t(word[4]) << 8) | word[5],
                5};
      case 7:
        return {(std::uint64_t(word[0]) << 48) | (std::uint64_t(word[1]) << 40) |
                    (std::uint64_t(word[2]) << 32) | (std::uint64_t(word[3]) << 24) |
                    (std::uint64_t(word[4]) << 16) | (std::uint64_t(word[5]) << 8) | word[6],
                6};
      default:
        return {pack8(word), 7};
    }
  }

  static std::uint64_t pack8(std::span<const std::uint8_t> key) {
    return (std::uint64_t(key[0]) << 56) | (std::uint64_t(key[1]) << 48) |
           (std::uint64_t(key[2]) << 40) | (std::uint64_t(key[3]) << 32) |
           (std::uint64_t(key[4]) << 24) | (std::uint64_t(key[5]) << 16) |
           (std::uint64_t(key[6]) << 8) | std::uint64_t(key[7]);
  }

  LongestResult find0(std::span<const std::uint8_t> key, std::uint64_t a) const {
    std::uint32_t index = 0;
    switch (key.size()) {
      case 0:
        return {};
      case 1:
        index = map1_[key[0]];
        if (index != kFalse) return {index, 1, true};
        return {};
      case 2:
        index = map2_[a];
        if (index != kFalse) return {index, 2, true};
        index = map1_[key[0]];
        if (index != kFalse) return {index, 1, true};
        return {};
      case 3: {
        auto it = map4_[0].find(static_cast<std::uint32_t>(a));
        if (it != map4_[0].end()) return {it->second, 3, true};
        a >>= 8;
        index = map2_[a];
        if (index != kFalse) return {index, 2, true};
        index = map1_[key[0]];
        if (index != kFalse) return {index, 1, true};
        return {};
      }
      case 4: {
        auto it = map4_[1].find(static_cast<std::uint32_t>(a));
        if (it != map4_[1].end()) return {it->second, 4, true};
        a >>= 8;
        it = map4_[0].find(static_cast<std::uint32_t>(a));
        if (it != map4_[0].end()) return {it->second, 3, true};
        a >>= 8;
        index = map2_[a];
        if (index != kFalse) return {index, 2, true};
        index = map1_[key[0]];
        if (index != kFalse) return {index, 1, true};
        return {};
      }
      case 5: {
        auto bit = (kFibonacci * a) >> 40;
        if ((bloom8_top4_[bit >> 6] & (std::uint64_t{1} << (bit & 63))) != 0) {
          auto it = map8_[0].find(a);
          if (it != map8_[0].end()) return {it->second, 5, true};
        }
        a >>= 8;
        auto it = map4_[1].find(static_cast<std::uint32_t>(a));
        if (it != map4_[1].end()) return {it->second, 4, true};
        a >>= 8;
        it = map4_[0].find(static_cast<std::uint32_t>(a));
        if (it != map4_[0].end()) return {it->second, 3, true};
        a >>= 8;
        index = map2_[a];
        if (index != kFalse) return {index, 2, true};
        index = map1_[key[0]];
        if (index != kFalse) return {index, 1, true};
        return {};
      }
      case 6: {
        auto bit = (kFibonacci * (a >> 8)) >> 40;
        if ((bloom8_top4_[bit >> 6] & (std::uint64_t{1} << (bit & 63))) != 0) {
          auto it8 = map8_[1].find(a);
          if (it8 != map8_[1].end()) return {it8->second, 6, true};
          a >>= 8;
          it8 = map8_[0].find(a);
          if (it8 != map8_[0].end()) return {it8->second, 5, true};
          a >>= 8;
        } else {
          a >>= 16;
        }
        auto it = map4_[1].find(static_cast<std::uint32_t>(a));
        if (it != map4_[1].end()) return {it->second, 4, true};
        a >>= 8;
        it = map4_[0].find(static_cast<std::uint32_t>(a));
        if (it != map4_[0].end()) return {it->second, 3, true};
        a >>= 8;
        index = map2_[a];
        if (index != kFalse) return {index, 2, true};
        index = map1_[key[0]];
        if (index != kFalse) return {index, 1, true};
        return {};
      }
      case 7: {
        auto bit = (kFibonacci * (a >> 16)) >> 40;
        if ((bloom8_top4_[bit >> 6] & (std::uint64_t{1} << (bit & 63))) != 0) {
          bit = (kFibonacci * a) >> 40;
          if ((bloom8_top2_[bit >> 6] & (std::uint64_t{1} << (bit & 63))) != 0) {
            auto it8 = map8_[2].find(a);
            if (it8 != map8_[2].end()) return {it8->second, 7, true};
          }
          a >>= 8;
          auto it8 = map8_[1].find(a);
          if (it8 != map8_[1].end()) return {it8->second, 6, true};
          a >>= 8;
          it8 = map8_[0].find(a);
          if (it8 != map8_[0].end()) return {it8->second, 5, true};
          a >>= 8;
        } else {
          a >>= 24;
        }
        auto it = map4_[1].find(static_cast<std::uint32_t>(a));
        if (it != map4_[1].end()) return {it->second, 4, true};
        a >>= 8;
        it = map4_[0].find(static_cast<std::uint32_t>(a));
        if (it != map4_[0].end()) return {it->second, 3, true};
        a >>= 8;
        index = map2_[a];
        if (index != kFalse) return {index, 2, true};
        index = map1_[key[0]];
        if (index != kFalse) return {index, 1, true};
        return {};
      }
      case 8: {
        auto bit = (kFibonacci * (a >> 24)) >> 40;
        if ((bloom8_top4_[bit >> 6] & (std::uint64_t{1} << (bit & 63))) != 0) {
          bit = (kFibonacci * (a >> 8)) >> 40;
          if ((bloom8_top2_[bit >> 6] & (std::uint64_t{1} << (bit & 63))) != 0) {
            auto it8 = map8_[3].find(a);
            if (it8 != map8_[3].end()) return {it8->second, 8, true};
            a >>= 8;
            it8 = map8_[2].find(a);
            if (it8 != map8_[2].end()) return {it8->second, 7, true};
            a >>= 8;
          } else {
            a >>= 16;
          }
          auto it8 = map8_[1].find(a);
          if (it8 != map8_[1].end()) return {it8->second, 6, true};
          a >>= 8;
          it8 = map8_[0].find(a);
          if (it8 != map8_[0].end()) return {it8->second, 5, true};
          a >>= 8;
        } else {
          a >>= 32;
        }
        auto it = map4_[1].find(static_cast<std::uint32_t>(a));
        if (it != map4_[1].end()) return {it->second, 4, true};
        a >>= 8;
        it = map4_[0].find(static_cast<std::uint32_t>(a));
        if (it != map4_[0].end()) return {it->second, 3, true};
        a >>= 8;
        index = map2_[a];
        if (index != kFalse) return {index, 2, true};
        index = map1_[key[0]];
        if (index != kFalse) return {index, 1, true};
        return {};
      }
      default:
        return {};
    }
  }

  LongestResult find1(Double ab, int l) const {
    std::uint32_t index = 0;
    if (l > 1) {
      auto bit = ((((kFnvOffset ^ (kFibonacci * (ab[1] >> ((std::uint64_t(l) - 2) * 8)))) *
                    kFnvPrime) ^
                   ab[0]) *
                  kFnvPrime) >>
                 40;
      if ((bloom16_top6_[bit >> 6] & (std::uint64_t{1} << (bit & 63))) == 0) {
        ab[1] >>= 8 * std::uint64_t(l - 1);
        l = 1;
      } else if (l > 3) {
        bit = ((((kFnvOffset ^ (kFibonacci * (ab[1] >> ((std::uint64_t(l) - 4) * 8)))) *
                 kFnvPrime) ^
                ab[0]) *
               kFnvPrime) >>
              40;
        if ((bloom16_top4_[bit >> 6] & (std::uint64_t{1} << (bit & 63))) == 0) {
          ab[1] >>= 8 * std::uint64_t(l - 3);
          l = 3;
        } else if (l > 5) {
          bit = ((((kFnvOffset ^ (kFibonacci * (ab[1] >> ((std::uint64_t(l) - 6) * 8)))) *
                   kFnvPrime) ^
                  ab[0]) *
                 kFnvPrime) >>
                40;
          if ((bloom16_top2_[bit >> 6] & (std::uint64_t{1} << (bit & 63))) == 0) {
            ab[1] >>= 8 * std::uint64_t(l - 5);
            l = 5;
          }
        }
      }
    }
    for (;;) {
      auto it = map16_[l].find(ab);
      if (it != map16_[l].end()) {
        index = it->second;
        return {index, 9 + l, true};
      }
      if (l == 0) return {0, 8, false};
      l--;
      ab[1] >>= 8;
    }
  }

  LongestResult find2(std::span<const std::uint8_t> key, std::uint64_t a, std::uint64_t b,
                      std::uint64_t c, int l) const {
    int length = static_cast<int>(key.size());
    for (;;) {
      if (auto result = binary_longest<3>(limit24_[l], {a, b, c}, count_[l + 16], length);
          result.found) {
        return result;
      }
      if (length == 17) return {0, length, false};
      length--;
      std::tie(c, l) = bytes2uint64(key.subspan(16, static_cast<std::size_t>(length - 16)));
    }
  }

  LongestResult find3(std::span<const std::uint8_t> key, std::uint64_t a, std::uint64_t b,
                      std::uint64_t c, std::uint64_t d, int l) const {
    int length = static_cast<int>(key.size());
    for (;;) {
      if (auto result = binary_longest<4>(limit32_[l], {a, b, c, d}, count_[l + 24], length);
          result.found) {
        return result;
      }
      if (length == 25) return {0, length, false};
      length--;
      std::tie(d, l) = bytes2uint64(key.subspan(24, static_cast<std::size_t>(length - 24)));
    }
  }

  LongestResult find4(std::span<const std::uint8_t> key, std::uint64_t a, std::uint64_t b,
                      std::uint64_t c, std::uint64_t d, std::uint64_t e, int l) const {
    int length = static_cast<int>(key.size());
    for (;;) {
      if (auto result =
              binary_longest<5>(limit40_[l], {a, b, c, d, e}, count_[l + 32], length);
          result.found) {
        return result;
      }
      if (length == 33) return {0, length, false};
      length--;
      std::tie(e, l) = bytes2uint64(key.subspan(32, static_cast<std::size_t>(length - 32)));
    }
  }

  template <std::size_t N>
  static std::pair<std::uint32_t, bool> binary_find(
      const std::vector<std::array<std::uint64_t, N>>& cur,
      const std::array<std::uint64_t, N>& target, std::uint32_t count) {
    int min = 0;
    int max = static_cast<int>(cur.size()) - 1;
    while (min <= max) {
      int at = min + ((max - min) / 2);
      if (target < cur[at]) {
        max = at - 1;
        continue;
      }
      if (cur[at] < target) {
        min = at + 1;
        continue;
      }
      return {static_cast<std::uint32_t>(at) + count, true};
    }
    return {static_cast<std::uint32_t>(min) + count, false};
  }

  template <std::size_t N>
  LongestResult binary_longest(const std::vector<std::array<std::uint64_t, N>>& cur,
                               const std::array<std::uint64_t, N>& target, std::uint32_t count,
                               int length) const {
    auto [index, found] = binary_find<N>(cur, target, count);
    return {index, length, found};
  }

  template <std::size_t N>
  static void sort_limit(
      std::array<std::vector<std::array<std::uint64_t, N>>, 8>& limit,
      std::array<std::vector<int>, 8>& order) {
    std::vector<KeyVal<std::array<std::uint64_t, N>>> temp;
    for (int run = 0; run < 8; run++) {
      int l = static_cast<int>(limit[run].size());
      if (l > 0) {
        auto& m = order[run];
        if (l != static_cast<int>(m.size())) throw Error("Build can only be run once");
        temp.resize(static_cast<std::size_t>(l));
        for (int z = 0; z < l; ++z) temp[z] = {m[z], limit[run][z]};
        m.clear();
        std::sort(temp.begin(), temp.end(), by_value<std::array<std::uint64_t, N>>);
        auto& newkey = limit[run];
        for (int i = 0; i < l; ++i) newkey[i] = temp[i].value;
      }
    }
  }

  static constexpr std::uint64_t kFnvOffset = 14695981039346656037ULL;
  static constexpr std::uint64_t kFnvPrime = 1099511628211ULL;
  static constexpr std::uint64_t kFibonacci = 11400714819323198485ULL;
  static constexpr std::uint32_t kFalse = 4294967295U;

  std::array<std::vector<std::uint64_t>, 8> limit8_{};
  std::array<std::vector<Double>, 8> limit16_{};
  std::array<std::vector<std::array<std::uint64_t, 3>>, 8> limit24_{};
  std::array<std::vector<std::array<std::uint64_t, 4>>, 8> limit32_{};
  std::array<std::vector<std::array<std::uint64_t, 5>>, 8> limit40_{};
  std::array<std::vector<int>, 8> order8_{};
  std::array<std::vector<int>, 8> order16_{};
  std::array<std::vector<int>, 8> order24_{};
  std::array<std::vector<int>, 8> order32_{};
  std::array<std::vector<int>, 8> order40_{};
  std::array<std::uint32_t, 41> count_{};
  int total_ = 0;
  std::array<std::uint64_t, 262144> bloom8_top4_{};
  std::array<std::uint64_t, 262144> bloom8_top2_{};
  std::array<std::uint64_t, 262144> bloom16_{};
  std::array<std::uint64_t, 262144> bloom16_top6_{};
  std::array<std::uint64_t, 262144> bloom16_top4_{};
  std::array<std::uint64_t, 262144> bloom16_top2_{};
  std::array<std::uint64_t, 262144> bloom24_{};
  std::array<std::uint64_t, 262144> bloom32_{};
  std::array<std::uint64_t, 262144> bloom40_{};
  std::array<std::uint64_t, 262144> bloom_big_skip_{};
  std::array<std::unordered_map<std::uint32_t, std::uint32_t>, 2> map4_{};
  std::array<std::unordered_map<std::uint64_t, std::uint32_t>, 4> map8_{};
  std::array<std::unordered_map<Double, std::uint32_t, DoubleHash>, 8> map16_{};
  std::array<std::uint32_t, 256> map1_{};
  std::array<std::uint32_t, 65536> map2_{};
};

Vocab::Vocab() = default;
Vocab::~Vocab() = default;
Vocab::Vocab(Vocab&&) noexcept = default;
Vocab& Vocab::operator=(Vocab&&) noexcept = default;

Vocab Vocab::load(const std::filesystem::path& path) {
  Reader r(path);
  Vocab res;
  res.using_capcode_ = r.read_byte();
  res.charset_ = r.read_byte();
  res.normalizer_flag_ = r.read_byte();
  res.level_ = r.read_byte();
  res.reserve_ = r.read_byte();
  r.read_byte();
  r.read_byte();
  r.read_byte();

  if (res.charset_ > 2 || res.using_capcode_ > 2) {
    throw Error("not a valid TokenMonster vocabulary");
  }

  res.unk_token_ = r.read_uint24();
  res.vocab_size_ = static_cast<int>(r.read_uint24());
  auto n_reverse = r.read_uint24();
  auto n_info = static_cast<int>(r.read_uint24());
  res.delete_token_ = r.read_uint24();
  res.max_token_length_ = static_cast<int>(r.read_byte());

  res.info_.resize(static_cast<std::size_t>(n_info));
  res.reverse_.resize(static_cast<std::size_t>(n_reverse));
  res.dictionary_ = std::make_unique<FastDictionary>();
  std::vector<int> lengths(static_cast<std::size_t>(n_info));

  for (int i = 0; i < n_info; ++i) {
    TokenInfo token;
    auto key = r.read_bytes8();
    lengths[static_cast<std::size_t>(i)] = static_cast<int>(key.size());
    if (key.size() > 40) throw Error("not a valid TokenMonster vocabulary");
    token.token = key;
    res.dictionary_->add(key);
    token.alt.data.flag = r.read_byte();
    token.alt.data.n_words = r.read_byte();
    token.alt.index = r.read_uint24();
    if (token.alt.index != does_not_exist) {
      token.alt.length = lengths[token.alt.index];
      token.alt.id1 = res.info_[token.alt.index].alt.id;
    }
    token.alt.index2 = r.read_uint24();
    if (token.alt.index2 != does_not_exist) {
      token.alt.length2 = lengths[token.alt.index2];
      token.alt.id2 = res.info_[token.alt.index2].alt.id;
    }
    token.alt.id = r.read_uint24();
    token.score = r.read_float32();
    if (token.alt.id >= res.reverse_.size()) throw Error("not a valid TokenMonster vocabulary");
    res.info_[static_cast<std::size_t>(i)] = token;
    res.reverse_[token.alt.id] = key;
  }

  for (auto& b : res.begin_byte_) b = r.read_byte();

  auto deleted_count = static_cast<int>(r.read_uint24());
  res.deleted_.resize(static_cast<std::size_t>(deleted_count));
  for (int i = 0; i < deleted_count; ++i) {
    res.deleted_[static_cast<std::size_t>(i)].token = r.read_bytes8();
    res.deleted_[static_cast<std::size_t>(i)].id = r.read_uint24();
    res.deleted_[static_cast<std::size_t>(i)].score = r.read_float32();
  }
  if (!r.eof()) throw Error("not a valid TokenMonster vocabulary");
  res.dictionary_->build();
  for (std::uint32_t i = 0; i < res.info_.size(); ++i) {
    auto [index, found] = res.dictionary_->find(res.info_[i].token);
    if (!found || index != i) {
      throw Error("dictionary order does not match vocabulary info order");
    }
  }
  return res;
}

std::vector<std::uint8_t> Vocab::normalize(std::span<const std::uint8_t> data) const {
  return normalize_and_capcode(data, using_capcode_, normalizer_flag_);
}

std::vector<std::uint32_t> Vocab::deserialize(std::span<const std::uint8_t> data,
                                              std::uint8_t encoding_length) const {
  if (encoding_length == 0) {
    encoding_length = reverse_.size() <= 65536 ? 2 : 3;
  }
  std::vector<std::uint32_t> tokens;
  if (encoding_length == 2) {
    tokens.resize(data.size() / 2);
    for (std::size_t i = 0; i + 1 < data.size(); i += 2) {
      tokens[i >> 1] = static_cast<std::uint32_t>(data[i]) |
                       (static_cast<std::uint32_t>(data[i + 1]) << 8);
    }
  } else if (encoding_length == 3) {
    tokens.resize(data.size() / 3);
    std::size_t on = 0;
    for (std::size_t i = 0; i + 2 < data.size(); i += 3) {
      tokens[on++] = static_cast<std::uint32_t>(data[i]) |
                     (static_cast<std::uint32_t>(data[i + 1]) << 8) |
                     (static_cast<std::uint32_t>(data[i + 2]) << 16);
    }
  } else if (encoding_length == 4) {
    tokens.resize(data.size() / 4);
    for (std::size_t i = 0; i + 3 < data.size(); i += 4) {
      tokens[i >> 2] = static_cast<std::uint32_t>(data[i]) |
                       (static_cast<std::uint32_t>(data[i + 1]) << 8) |
                       (static_cast<std::uint32_t>(data[i + 2]) << 16) |
                       (static_cast<std::uint32_t>(data[i + 3]) << 24);
    }
  }
  return tokens;
}

std::vector<std::uint32_t> Decoder::deserialize(std::span<const std::uint8_t> data,
                                                std::uint8_t encoding_length) const {
  if (!vocab_) throw Error("decoder has no vocabulary");
  return vocab_->deserialize(data, encoding_length);
}

std::vector<std::uint8_t> Vocab::decode_raw(std::span<const std::uint32_t> tokens) const {
  std::size_t size = 0;
  for (auto id : tokens) {
    if (id < reverse_.size()) size += reverse_[id].size();
  }
  Bytes data(size);
  std::size_t i = 0;
  for (auto id : tokens) {
    if (id < reverse_.size()) {
      std::copy(reverse_[id].begin(), reverse_[id].end(),
                data.begin() + static_cast<std::ptrdiff_t>(i));
      i += reverse_[id].size();
    }
  }
  return data;
}

std::vector<std::uint8_t> Vocab::decode(std::span<const std::uint32_t> tokens) const {
  auto data = decode_raw(tokens);
  if (using_capcode_ == 2) return capcode::decode(std::move(data));
  if (using_capcode_ == 1) return capcode::no_capcode_decode(std::move(data));
  return data;
}

std::vector<std::uint8_t> Vocab::decode_serialized_raw(std::span<const std::uint8_t> data,
                                                       std::uint8_t encoding_length) const {
  if (encoding_length <= 1) {
    encoding_length = reverse_.size() <= 65536 ? 2 : 3;
  }
  Bytes buffer;
  if (encoding_length == 2) {
    if (reverse_.empty()) return {};
    auto n_tokens = static_cast<std::uint16_t>(reverse_.size() - 1);
    std::size_t size = 0;
    for (std::size_t i = 0; i + 1 < data.size(); i += 2) {
      auto v = static_cast<std::uint16_t>(data[i] | (data[i + 1] << 8));
      if (v <= n_tokens) size += reverse_[v].size();
    }
    buffer.resize(size);
    size = 0;
    for (std::size_t i = 0; i + 1 < data.size(); i += 2) {
      auto v = static_cast<std::uint16_t>(data[i] | (data[i + 1] << 8));
      if (v <= n_tokens) {
        std::copy(reverse_[v].begin(), reverse_[v].end(),
                  buffer.begin() + static_cast<std::ptrdiff_t>(size));
        size += reverse_[v].size();
      }
    }
    return buffer;
  }
  if (encoding_length == 3) {
    auto n_tokens = static_cast<std::uint32_t>(reverse_.size());
    std::size_t size = 0;
    for (std::size_t i = 0; i + 2 < data.size(); i += 3) {
      auto v = static_cast<std::uint32_t>(data[i]) |
               (static_cast<std::uint32_t>(data[i + 1]) << 8) |
               (static_cast<std::uint32_t>(data[i + 2]) << 16);
      if (v < n_tokens) size += reverse_[v].size();
    }
    buffer.resize(size);
    size = 0;
    for (std::size_t i = 0; i + 2 < data.size(); i += 3) {
      auto v = static_cast<std::uint32_t>(data[i]) |
               (static_cast<std::uint32_t>(data[i + 1]) << 8) |
               (static_cast<std::uint32_t>(data[i + 2]) << 16);
      if (v < n_tokens) {
        std::copy(reverse_[v].begin(), reverse_[v].end(),
                  buffer.begin() + static_cast<std::ptrdiff_t>(size));
        size += reverse_[v].size();
      }
    }
    return buffer;
  }
  if (encoding_length == 4) {
    auto n_tokens = static_cast<std::uint32_t>(reverse_.size());
    std::size_t size = 0;
    for (std::size_t i = 0; i + 3 < data.size(); i += 4) {
      auto v = static_cast<std::uint32_t>(data[i]) |
               (static_cast<std::uint32_t>(data[i + 1]) << 8) |
               (static_cast<std::uint32_t>(data[i + 2]) << 16) |
               (static_cast<std::uint32_t>(data[i + 3]) << 24);
      if (v < n_tokens) size += reverse_[v].size();
    }
    buffer.resize(size);
    size = 0;
    for (std::size_t i = 0; i + 3 < data.size(); i += 4) {
      auto v = static_cast<std::uint32_t>(data[i]) |
               (static_cast<std::uint32_t>(data[i + 1]) << 8) |
               (static_cast<std::uint32_t>(data[i + 2]) << 16) |
               (static_cast<std::uint32_t>(data[i + 3]) << 24);
      if (v < n_tokens) {
        std::copy(reverse_[v].begin(), reverse_[v].end(),
                  buffer.begin() + static_cast<std::ptrdiff_t>(size));
        size += reverse_[v].size();
      }
    }
    return buffer;
  }
  return {};
}

std::vector<std::uint8_t> Vocab::decode_serialized(std::span<const std::uint8_t> data,
                                                   std::uint8_t encoding_length) const {
  auto decoded = decode_serialized_raw(data, encoding_length);
  if (using_capcode_ == 2) return capcode::decode(std::move(decoded));
  if (using_capcode_ == 1) return capcode::no_capcode_decode(std::move(decoded));
  return decoded;
}

std::vector<std::uint8_t> Decoder::decode(std::span<const std::uint32_t> tokens) {
  if (!vocab_) throw Error("decoder has no vocabulary");
  if (vocab_->charset_ == 0) return vocab_->decode_raw(tokens);

  std::size_t size = remainder_.size();
  for (auto id : tokens) {
    if (id < vocab_->reverse_.size()) size += vocab_->reverse_[id].size();
  }
  Bytes data(size);
  std::copy(remainder_.begin(), remainder_.end(), data.begin());
  std::size_t i = remainder_.size();
  for (auto id : tokens) {
    if (id < vocab_->reverse_.size()) {
      std::copy(vocab_->reverse_[id].begin(), vocab_->reverse_[id].end(),
                data.begin() + static_cast<std::ptrdiff_t>(i));
      i += vocab_->reverse_[id].size();
    }
  }

  int incomplete =
      vocab_->charset_ == 1 ? incomplete_utf8_bytes(data) : incomplete_utf16_bytes(data);
  std::size_t keep = data.size() - static_cast<std::size_t>(incomplete);
  remainder_ = Bytes(data.begin() + static_cast<std::ptrdiff_t>(keep), data.end());
  data.resize(keep);

  if (vocab_->using_capcode_ == 2) return capcode_decoder_.decode(data);
  if (vocab_->using_capcode_ == 1) return capcode_decoder_.no_capcode_decode(data);
  return data;
}

std::vector<std::uint8_t> Decoder::decode_serialized(std::span<const std::uint8_t> data,
                                                     std::uint8_t encoding_length) {
  if (!vocab_) throw Error("decoder has no vocabulary");
  if (encoding_length <= 1) {
    if (vocab_->reverse_.size() <= 65536) {
      encoding_length = 2;
    } else {
      encoding_length = 3;
    }
  }
  if (encoding_length == 2) {
    const auto& reverse = vocab_->reverse_;
    if (reverse.empty()) return {};
    auto n_tokens = static_cast<std::uint16_t>(reverse.size() - 1);
    std::size_t i = 0;
    if (vocab_->charset_ == 0) {
      for (std::size_t on = 0; on + 1 < data.size(); on += 2) {
        auto v = static_cast<std::uint16_t>(data[on] | (data[on + 1] << 8));
        if (v <= n_tokens) i += reverse[v].size();
      }
      Bytes buffer(i);
      i = 0;
      for (std::size_t on = 0; on + 1 < data.size(); on += 2) {
        auto v = static_cast<std::uint16_t>(data[on] | (data[on + 1] << 8));
        if (v <= n_tokens) {
          std::copy(reverse[v].begin(), reverse[v].end(),
                    buffer.begin() + static_cast<std::ptrdiff_t>(i));
          i += reverse[v].size();
        }
      }
      return buffer;
    }
    i = remainder_.size();
    for (std::size_t on = 0; on + 1 < data.size(); on += 2) {
      auto v = static_cast<std::uint16_t>(data[on] | (data[on + 1] << 8));
      if (v <= n_tokens) i += reverse[v].size();
    }
    Bytes buffer(i);
    std::copy(remainder_.begin(), remainder_.end(), buffer.begin());
    i = remainder_.size();
    for (std::size_t on = 0; on + 1 < data.size(); on += 2) {
      auto v = static_cast<std::uint16_t>(data[on] | (data[on + 1] << 8));
      if (v <= n_tokens) {
        std::copy(reverse[v].begin(), reverse[v].end(),
                  buffer.begin() + static_cast<std::ptrdiff_t>(i));
        i += reverse[v].size();
      }
    }
    std::size_t remaining = buffer.size() - static_cast<std::size_t>(
                                                vocab_->charset_ == 1
                                                    ? incomplete_utf8_bytes(buffer)
                                                    : incomplete_utf16_bytes(buffer));
    remainder_ = Bytes(buffer.begin() + static_cast<std::ptrdiff_t>(remaining), buffer.end());
    buffer.resize(remaining);
    if (vocab_->using_capcode_ == 2) return capcode_decoder_.decode(buffer);
    if (vocab_->using_capcode_ == 1) return capcode_decoder_.no_capcode_decode(buffer);
    return buffer;
  } else if (encoding_length == 3) {
    std::size_t on = 0;
    std::uint32_t v = 0;
    const auto& reverse = vocab_->reverse_;
    auto n_tokens = static_cast<std::uint32_t>(reverse.size());
    std::size_t i = 0;
    if (vocab_->charset_ == 0) {
      for (on = 0; on + 2 < data.size(); on += 3) {
        v = static_cast<std::uint32_t>(data[on]) |
            (static_cast<std::uint32_t>(data[on + 1]) << 8) |
            (static_cast<std::uint32_t>(data[on + 2]) << 16);
        if (v < n_tokens) i += reverse[v].size();
      }
      Bytes buffer(i);
      i = 0;
      for (on = 0; on + 2 < data.size(); on += 3) {
        v = static_cast<std::uint32_t>(data[on]) |
            (static_cast<std::uint32_t>(data[on + 1]) << 8) |
            (static_cast<std::uint32_t>(data[on + 2]) << 16);
        if (v < n_tokens) {
          std::copy(reverse[v].begin(), reverse[v].end(),
                    buffer.begin() + static_cast<std::ptrdiff_t>(i));
          i += reverse[v].size();
        }
      }
      return buffer;
    }
    i = remainder_.size();
    for (on = 0; on + 2 < data.size(); on += 3) {
      v = static_cast<std::uint32_t>(data[on]) |
          (static_cast<std::uint32_t>(data[on + 1]) << 8) |
          (static_cast<std::uint32_t>(data[on + 2]) << 16);
      if (v < n_tokens) i += reverse[v].size();
    }
    Bytes buffer(i);
    std::copy(remainder_.begin(), remainder_.end(), buffer.begin());
    i = remainder_.size();
    for (on = 0; on + 2 < data.size(); on += 3) {
      v = static_cast<std::uint32_t>(data[on]) |
          (static_cast<std::uint32_t>(data[on + 1]) << 8) |
          (static_cast<std::uint32_t>(data[on + 2]) << 16);
      if (v < n_tokens) {
        std::copy(reverse[v].begin(), reverse[v].end(),
                  buffer.begin() + static_cast<std::ptrdiff_t>(i));
        i += reverse[v].size();
      }
    }
    std::size_t remaining = buffer.size() - static_cast<std::size_t>(
                                                vocab_->charset_ == 1
                                                    ? incomplete_utf8_bytes(buffer)
                                                    : incomplete_utf16_bytes(buffer));
    remainder_ = Bytes(buffer.begin() + static_cast<std::ptrdiff_t>(remaining), buffer.end());
    buffer.resize(remaining);
    if (vocab_->using_capcode_ == 2) return capcode_decoder_.decode(buffer);
    if (vocab_->using_capcode_ == 1) return capcode_decoder_.no_capcode_decode(buffer);
    return buffer;
  } else if (encoding_length == 4) {
    const auto& reverse = vocab_->reverse_;
    auto n_tokens = static_cast<std::uint32_t>(reverse.size());
    std::size_t i = 0;
    if (vocab_->charset_ == 0) {
      for (std::size_t on = 0; on + 3 < data.size(); on += 4) {
        auto v = static_cast<std::uint32_t>(data[on]) |
                 (static_cast<std::uint32_t>(data[on + 1]) << 8) |
                 (static_cast<std::uint32_t>(data[on + 2]) << 16) |
                 (static_cast<std::uint32_t>(data[on + 3]) << 24);
        if (v < n_tokens) i += reverse[v].size();
      }
      Bytes buffer(i);
      i = 0;
      for (std::size_t on = 0; on + 3 < data.size(); on += 4) {
        auto v = static_cast<std::uint32_t>(data[on]) |
                 (static_cast<std::uint32_t>(data[on + 1]) << 8) |
                 (static_cast<std::uint32_t>(data[on + 2]) << 16) |
                 (static_cast<std::uint32_t>(data[on + 3]) << 24);
        if (v < n_tokens) {
          std::copy(reverse[v].begin(), reverse[v].end(),
                    buffer.begin() + static_cast<std::ptrdiff_t>(i));
          i += reverse[v].size();
        }
      }
      return buffer;
    }
    i = remainder_.size();
    for (std::size_t on = 0; on + 3 < data.size(); on += 4) {
      auto v = static_cast<std::uint32_t>(data[on]) |
               (static_cast<std::uint32_t>(data[on + 1]) << 8) |
               (static_cast<std::uint32_t>(data[on + 2]) << 16) |
               (static_cast<std::uint32_t>(data[on + 3]) << 24);
      if (v < n_tokens) i += reverse[v].size();
    }
    Bytes buffer(i);
    std::copy(remainder_.begin(), remainder_.end(), buffer.begin());
    i = remainder_.size();
    for (std::size_t on = 0; on + 3 < data.size(); on += 4) {
      auto v = static_cast<std::uint32_t>(data[on]) |
               (static_cast<std::uint32_t>(data[on + 1]) << 8) |
               (static_cast<std::uint32_t>(data[on + 2]) << 16) |
               (static_cast<std::uint32_t>(data[on + 3]) << 24);
      if (v < n_tokens) {
        std::copy(reverse[v].begin(), reverse[v].end(),
                  buffer.begin() + static_cast<std::ptrdiff_t>(i));
        i += reverse[v].size();
      }
    }
    std::size_t remaining = buffer.size() - static_cast<std::size_t>(
                                                vocab_->charset_ == 1
                                                    ? incomplete_utf8_bytes(buffer)
                                                    : incomplete_utf16_bytes(buffer));
    remainder_ = Bytes(buffer.begin() + static_cast<std::ptrdiff_t>(remaining), buffer.end());
    buffer.resize(remaining);
    if (vocab_->using_capcode_ == 2) return capcode_decoder_.decode(buffer);
    if (vocab_->using_capcode_ == 1) return capcode_decoder_.no_capcode_decode(buffer);
    return buffer;
  }
  return {};
}

std::vector<std::uint8_t> Decoder::flush() {
  auto out = remainder_;
  remainder_.clear();
  return out;
}

TokenizeResult Vocab::tokenize_normalized(std::span<const std::uint8_t> normalized) const {
  Bytes data(normalized.begin(), normalized.end());
  const int len_data = static_cast<int>(data.size());
  data.push_back(0);

  int i = 0, i1 = 0, i2 = 0, i3 = 0;
  int length = 0, length1 = 0, length2 = 0, length3 = 0;
  int length1b = 0, length2b = 0, length3b = 0;
  std::uint32_t index = 0, index1 = 0, index2 = 0, index3 = 0;
  std::uint32_t index1b = 0, index2b = 0, index3b = 0;
  int branch_length = 0, n_words = 0;
  bool found = false, found1 = false, found2 = false, found3 = false;
  int score1 = 0, score2 = 0, score3 = 0, score1b = 0, score2b = 0, score3b = 0;
  int max_score = 0;
  int forward_delete = 0;
  std::uint8_t next_byte = 0;
  TokenOuter original;
  TokenInner first, second;
  std::vector<std::uint32_t> tokens;
  tokens.reserve(static_cast<std::size_t>((len_data / 4) + 4));
  int missing = 0;

  Bytes lilbuf(static_cast<std::size_t>(max_token_length_), 0);
  if (!lilbuf.empty()) lilbuf[0] = 32;
  int lilbuf_offset = charset_ == 2 ? 2 : 1;
  int max_token_length_with_space = max_token_length_ - lilbuf_offset;

  while (i < len_data) {
    auto longest = dictionary_->longest_substring(
        std::span<const std::uint8_t>(data).subspan(
            static_cast<std::size_t>(i),
            static_cast<std::size_t>(min_int(len_data - i, max_token_length_))));
    index = longest.index;
    length = longest.length;
    found = longest.found;
    if (found) {
    checkpoint:
      original = info_[index].alt;
      i1 = i + length;
      if (i1 < len_data && ((original.data.flag & 32) == 0 || begin_byte_[data[i1]] != 12)) {
        score1 = score2 = score3 = score1b = score2b = score3b = -1000000;
        max_score = -1000000;

        auto l1 = dictionary_->longest_substring(
            std::span<const std::uint8_t>(data).subspan(
                static_cast<std::size_t>(i1),
                static_cast<std::size_t>(min_int(len_data - i1, max_token_length_))));
        index1 = l1.index;
        length1 = l1.length;
        found1 = l1.found;

        if (found1) {
          n_words = int(original.data.n_words) - forward_delete;
          second = info_[index1].alt.data;
          next_byte = begin_byte_[data[i1 + length1]];
          score1 = ((length + length1 + int((original.data.flag >> 7) + (second.flag >> 7)) +
                     max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                     int((second.flag >> 2) & 1) + int((next_byte >> 2) & 1) +
                     ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                    ((int(original.data.flag & 1 & (second.flag >> 1)) * 103) +
                     (int((original.data.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                     (int(second.flag & 1 & next_byte) * 3)));
          max_score = score1;

          if (delete_token_ != does_not_exist && (second.flag & 2) != 0 && next_byte == 1 &&
              second.n_words == 0) {
            length1b = min_int(len_data - i1, max_token_length_with_space);
            std::fill(lilbuf.begin() + lilbuf_offset, lilbuf.end(), 0);
            std::copy_n(data.begin() + i1, length1b, lilbuf.begin() + lilbuf_offset);
            auto l1b = dictionary_->longest_substring(std::span<const std::uint8_t>(
                lilbuf.data(), static_cast<std::size_t>(length1b + lilbuf_offset)));
            index1b = l1b.index;
            length1b = l1b.length;
            if (length1b > length1 + 1) {
              length1b -= lilbuf_offset;
              second = info_[index1b].alt.data;
              next_byte = begin_byte_[data[i1 + length1b]];
              score1b = ((length + length1b + int((original.data.flag >> 7) + (second.flag >> 7)) +
                          max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                          int((next_byte >> 2) & 1) +
                          ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                         ((int(original.data.flag & 1) * 103) +
                          (int((original.data.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                          (int(second.flag & 1 & next_byte) * 3) + 1));
              max_score = max_int(max_score, score1b);
            }
          }
        }

        if (original.index != does_not_exist) {
          i2 = i + original.length - forward_delete;
          auto l2 = dictionary_->longest_substring(
              std::span<const std::uint8_t>(data).subspan(
                  static_cast<std::size_t>(i2),
                  static_cast<std::size_t>(min_int(len_data - i2, max_token_length_))));
          index2 = l2.index;
          length2 = l2.length;
          found2 = l2.found;

          if (found2) {
            first = info_[original.index].alt.data;
            n_words = int(first.n_words) - forward_delete;
            second = info_[index2].alt.data;
            next_byte = begin_byte_[data[i2 + length2]];
            branch_length = original.length + length2 - forward_delete;
            score2 = ((branch_length + int((first.flag >> 7) + (second.flag >> 7)) +
                       max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                       int((second.flag >> 2) & 1) + int((next_byte >> 2) & 1) +
                       ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                      ((int(first.flag & 1 & (second.flag >> 1)) * 103) +
                       (int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                       (int(second.flag & 1 & next_byte) * 3) +
                       (less_than(branch_length, length) * 100) +
                       (equal_to(branch_length, length) * 10000)));
            max_score = max_int(max_score, score2);

            if (delete_token_ != does_not_exist && (second.flag & 2) != 0 && next_byte == 1 &&
                second.n_words == 0) {
              length2b = min_int(len_data - i2, max_token_length_with_space);
              std::fill(lilbuf.begin() + lilbuf_offset, lilbuf.end(), 0);
              std::copy_n(data.begin() + i2, length2b, lilbuf.begin() + lilbuf_offset);
              auto l2b = dictionary_->longest_substring(std::span<const std::uint8_t>(
                  lilbuf.data(), static_cast<std::size_t>(length2b + lilbuf_offset)));
              index2b = l2b.index;
              length2b = l2b.length;
              if (length2b > length2 + 1) {
                length2b -= lilbuf_offset;
                second = info_[index2b].alt.data;
                branch_length = original.length + length2b - forward_delete;
                next_byte = begin_byte_[data[i2 + length2b]];
                score2b =
                    ((branch_length + int((first.flag >> 7) + (second.flag >> 7)) +
                      max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                      int((next_byte >> 2) & 1) +
                      ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                     ((int(first.flag & 1) * 103) +
                      (int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                      (int(second.flag & 1 & next_byte) * 3) + 1 +
                      (less_than(branch_length, length) * 100) +
                      (equal_to(branch_length, length) * 10000)));
                max_score = max_int(max_score, score2b);
              }
            }
          }

          if (original.index2 != does_not_exist) {
            i3 = i + original.length2 - forward_delete;
            auto l3 = dictionary_->longest_substring(
                std::span<const std::uint8_t>(data).subspan(
                    static_cast<std::size_t>(i3),
                    static_cast<std::size_t>(min_int(len_data - i3, max_token_length_))));
            index3 = l3.index;
            length3 = l3.length;
            found3 = l3.found;

            if (found3) {
              first = info_[original.index2].alt.data;
              n_words = int(first.n_words) - forward_delete;
              second = info_[index3].alt.data;
              next_byte = begin_byte_[data[i3 + length3]];
              branch_length = original.length2 + length3 - forward_delete;
              score3 = ((branch_length + int((first.flag >> 7) + (second.flag >> 7)) +
                         max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                         int((second.flag >> 2) & 1) + int((next_byte >> 2) & 1) +
                         ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                        ((int(first.flag & 1 & (second.flag >> 1)) * 103) +
                         (int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                         (int(second.flag & 1 & next_byte) * 3) +
                         (less_than(branch_length, length) * 100) +
                         (equal_to(branch_length, length) * 10000)));
              max_score = max_int(max_score, score3);

              if (delete_token_ != does_not_exist && (second.flag & 2) != 0 && next_byte == 1 &&
                  second.n_words == 0) {
                length3b = min_int(len_data - i3, max_token_length_with_space);
                std::fill(lilbuf.begin() + lilbuf_offset, lilbuf.end(), 0);
                std::copy_n(data.begin() + i3, length3b, lilbuf.begin() + lilbuf_offset);
                auto l3b = dictionary_->longest_substring(std::span<const std::uint8_t>(
                    lilbuf.data(), static_cast<std::size_t>(length3b + lilbuf_offset)));
                index3b = l3b.index;
                length3b = l3b.length;
                if (length3b > length3 + 1) {
                  length3b -= lilbuf_offset;
                  second = info_[index3b].alt.data;
                  branch_length = original.length2 + length3b - forward_delete;
                  next_byte = begin_byte_[data[i3 + length3b]];
                  score3b =
                      ((branch_length + int((first.flag >> 7) + (second.flag >> 7)) +
                        max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                        int((next_byte >> 2) & 1) +
                        ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                       ((int(first.flag & 1) * 103) +
                        (int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                        (int(second.flag & 1 & next_byte) * 3) + 1 +
                        (less_than(branch_length, length) * 100) +
                        (equal_to(branch_length, length) * 10000)));
                  max_score = max_int(max_score, score3b);
                }
              }
            }
          }
        }

        if (max_score != -1000000) {
          if (max_score == score1) {
            tokens.push_back(original.id);
            i += length;
            length = length1;
            index = index1;
            forward_delete = 0;
            goto checkpoint;
          }
          if (max_score == score2) {
            tokens.push_back(original.id1);
            i += original.length - forward_delete;
            length = length2;
            index = index2;
            forward_delete = 0;
            goto checkpoint;
          }
          if (max_score == score3) {
            tokens.push_back(original.id2);
            i += original.length2 - forward_delete;
            length = length3;
            index = index3;
            forward_delete = 0;
            goto checkpoint;
          }
          if (max_score == score1b) {
            tokens.push_back(original.id);
            tokens.push_back(delete_token_);
            i += length;
            length = length1b;
            index = index1b;
            forward_delete = 1;
            goto checkpoint;
          }
          if (max_score == score2b) {
            tokens.push_back(original.id1);
            tokens.push_back(delete_token_);
            i += original.length - forward_delete;
            length = length2b;
            index = index2b;
            forward_delete = 1;
            goto checkpoint;
          }
          if (max_score == score3b) {
            tokens.push_back(original.id2);
            tokens.push_back(delete_token_);
            i += original.length2 - forward_delete;
            length = length3b;
            index = index3b;
            forward_delete = 1;
            goto checkpoint;
          }
        }
      }
      tokens.push_back(original.id);
      i += length;
      forward_delete = 0;
    } else {
      if (unk_token_ != does_not_exist) tokens.push_back(unk_token_);
      ++i;
      ++missing;
      forward_delete = 0;
    }
  }
  return {std::move(tokens), missing};
}


CountResult Vocab::tokenize_count_normalized(std::span<const std::uint8_t> normalized) const {
  Bytes data(normalized.begin(), normalized.end());
  const int len_data = static_cast<int>(data.size());
  data.push_back(0);

  int i = 0, i1 = 0, i2 = 0, i3 = 0;
  int length = 0, length1 = 0, length2 = 0, length3 = 0;
  int length1b = 0, length2b = 0, length3b = 0;
  std::uint32_t index = 0, index1 = 0, index2 = 0, index3 = 0;
  std::uint32_t index1b = 0, index2b = 0, index3b = 0;
  int branch_length = 0, n_words = 0;
  bool found = false, found1 = false, found2 = false, found3 = false;
  int score1 = 0, score2 = 0, score3 = 0, score1b = 0, score2b = 0, score3b = 0;
  int max_score = 0;
  int forward_delete = 0;
  std::uint8_t next_byte = 0;
  TokenOuter original;
  TokenInner first, second;
  int tokens = 0;
  int missing = 0;

  Bytes lilbuf(static_cast<std::size_t>(max_token_length_), 0);
  if (!lilbuf.empty()) lilbuf[0] = 32;
  int lilbuf_offset = charset_ == 2 ? 2 : 1;
  int max_token_length_with_space = max_token_length_ - lilbuf_offset;

  while (i < len_data) {
    auto longest = dictionary_->longest_substring(
        std::span<const std::uint8_t>(data).subspan(
            static_cast<std::size_t>(i),
            static_cast<std::size_t>(min_int(len_data - i, max_token_length_))));
    index = longest.index;
    length = longest.length;
    found = longest.found;
    if (found) {
    checkpoint:
      original = info_[index].alt;
      i1 = i + length;
      if (i1 < len_data && ((original.data.flag & 32) == 0 || begin_byte_[data[i1]] != 12)) {
        score1 = score2 = score3 = score1b = score2b = score3b = -1000000;
        max_score = -1000000;

        auto l1 = dictionary_->longest_substring(
            std::span<const std::uint8_t>(data).subspan(
                static_cast<std::size_t>(i1),
                static_cast<std::size_t>(min_int(len_data - i1, max_token_length_))));
        index1 = l1.index;
        length1 = l1.length;
        found1 = l1.found;

        if (found1) {
          n_words = int(original.data.n_words) - forward_delete;
          second = info_[index1].alt.data;
          next_byte = begin_byte_[data[i1 + length1]];
          score1 = ((length + length1 + int((original.data.flag >> 7) + (second.flag >> 7)) +
                     max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                     int((second.flag >> 2) & 1) + int((next_byte >> 2) & 1) +
                     ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                    ((int(original.data.flag & 1 & (second.flag >> 1)) * 103) +
                     (int((original.data.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                     (int(second.flag & 1 & next_byte) * 3)));
          max_score = score1;

          if (delete_token_ != does_not_exist && (second.flag & 2) != 0 && next_byte == 1 &&
              second.n_words == 0) {
            length1b = min_int(len_data - i1, max_token_length_with_space);
            std::fill(lilbuf.begin() + lilbuf_offset, lilbuf.end(), 0);
            std::copy_n(data.begin() + i1, length1b, lilbuf.begin() + lilbuf_offset);
            auto l1b = dictionary_->longest_substring(std::span<const std::uint8_t>(
                lilbuf.data(), static_cast<std::size_t>(length1b + lilbuf_offset)));
            index1b = l1b.index;
            length1b = l1b.length;
            if (length1b > length1 + 1) {
              length1b -= lilbuf_offset;
              second = info_[index1b].alt.data;
              next_byte = begin_byte_[data[i1 + length1b]];
              score1b = ((length + length1b + int((original.data.flag >> 7) + (second.flag >> 7)) +
                          max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                          int((next_byte >> 2) & 1) +
                          ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                         ((int(original.data.flag & 1) * 103) +
                          (int((original.data.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                          (int(second.flag & 1 & next_byte) * 3) + 1));
              max_score = max_int(max_score, score1b);
            }
          }
        }

        if (original.index != does_not_exist) {
          i2 = i + original.length - forward_delete;
          auto l2 = dictionary_->longest_substring(
              std::span<const std::uint8_t>(data).subspan(
                  static_cast<std::size_t>(i2),
                  static_cast<std::size_t>(min_int(len_data - i2, max_token_length_))));
          index2 = l2.index;
          length2 = l2.length;
          found2 = l2.found;

          if (found2) {
            first = info_[original.index].alt.data;
            n_words = int(first.n_words) - forward_delete;
            second = info_[index2].alt.data;
            next_byte = begin_byte_[data[i2 + length2]];
            branch_length = original.length + length2 - forward_delete;
            score2 = ((branch_length + int((first.flag >> 7) + (second.flag >> 7)) +
                       max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                       int((second.flag >> 2) & 1) + int((next_byte >> 2) & 1) +
                       ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                      ((int(first.flag & 1 & (second.flag >> 1)) * 103) +
                       (int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                       (int(second.flag & 1 & next_byte) * 3) +
                       (less_than(branch_length, length) * 100) +
                       (equal_to(branch_length, length) * 10000)));
            max_score = max_int(max_score, score2);

            if (delete_token_ != does_not_exist && (second.flag & 2) != 0 && next_byte == 1 &&
                second.n_words == 0) {
              length2b = min_int(len_data - i2, max_token_length_with_space);
              std::fill(lilbuf.begin() + lilbuf_offset, lilbuf.end(), 0);
              std::copy_n(data.begin() + i2, length2b, lilbuf.begin() + lilbuf_offset);
              auto l2b = dictionary_->longest_substring(std::span<const std::uint8_t>(
                  lilbuf.data(), static_cast<std::size_t>(length2b + lilbuf_offset)));
              index2b = l2b.index;
              length2b = l2b.length;
              if (length2b > length2 + 1) {
                length2b -= lilbuf_offset;
                second = info_[index2b].alt.data;
                branch_length = original.length + length2b - forward_delete;
                next_byte = begin_byte_[data[i2 + length2b]];
                score2b =
                    ((branch_length + int((first.flag >> 7) + (second.flag >> 7)) +
                      max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                      int((next_byte >> 2) & 1) +
                      ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                     ((int(first.flag & 1) * 103) +
                      (int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                      (int(second.flag & 1 & next_byte) * 3) + 1 +
                      (less_than(branch_length, length) * 100) +
                      (equal_to(branch_length, length) * 10000)));
                max_score = max_int(max_score, score2b);
              }
            }
          }

          if (original.index2 != does_not_exist) {
            i3 = i + original.length2 - forward_delete;
            auto l3 = dictionary_->longest_substring(
                std::span<const std::uint8_t>(data).subspan(
                    static_cast<std::size_t>(i3),
                    static_cast<std::size_t>(min_int(len_data - i3, max_token_length_))));
            index3 = l3.index;
            length3 = l3.length;
            found3 = l3.found;

            if (found3) {
              first = info_[original.index2].alt.data;
              n_words = int(first.n_words) - forward_delete;
              second = info_[index3].alt.data;
              next_byte = begin_byte_[data[i3 + length3]];
              branch_length = original.length2 + length3 - forward_delete;
              score3 = ((branch_length + int((first.flag >> 7) + (second.flag >> 7)) +
                         max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                         int((second.flag >> 2) & 1) + int((next_byte >> 2) & 1) +
                         ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                        ((int(first.flag & 1 & (second.flag >> 1)) * 103) +
                         (int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                         (int(second.flag & 1 & next_byte) * 3) +
                         (less_than(branch_length, length) * 100) +
                         (equal_to(branch_length, length) * 10000)));
              max_score = max_int(max_score, score3);

              if (delete_token_ != does_not_exist && (second.flag & 2) != 0 && next_byte == 1 &&
                  second.n_words == 0) {
                length3b = min_int(len_data - i3, max_token_length_with_space);
                std::fill(lilbuf.begin() + lilbuf_offset, lilbuf.end(), 0);
                std::copy_n(data.begin() + i3, length3b, lilbuf.begin() + lilbuf_offset);
                auto l3b = dictionary_->longest_substring(std::span<const std::uint8_t>(
                    lilbuf.data(), static_cast<std::size_t>(length3b + lilbuf_offset)));
                index3b = l3b.index;
                length3b = l3b.length;
                if (length3b > length3 + 1) {
                  length3b -= lilbuf_offset;
                  second = info_[index3b].alt.data;
                  branch_length = original.length2 + length3b - forward_delete;
                  next_byte = begin_byte_[data[i3 + length3b]];
                  score3b =
                      ((branch_length + int((first.flag >> 7) + (second.flag >> 7)) +
                        max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                        int((next_byte >> 2) & 1) +
                        ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                       ((int(first.flag & 1) * 103) +
                        (int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                        (int(second.flag & 1 & next_byte) * 3) + 1 +
                        (less_than(branch_length, length) * 100) +
                        (equal_to(branch_length, length) * 10000)));
                  max_score = max_int(max_score, score3b);
                }
              }
            }
          }
        }

        if (max_score != -1000000) {
          if (max_score == score1) {
            tokens++;
            i += length;
            length = length1;
            index = index1;
            forward_delete = 0;
            goto checkpoint;
          }
          if (max_score == score2) {
            tokens++;
            i += original.length - forward_delete;
            length = length2;
            index = index2;
            forward_delete = 0;
            goto checkpoint;
          }
          if (max_score == score3) {
            tokens++;
            i += original.length2 - forward_delete;
            length = length3;
            index = index3;
            forward_delete = 0;
            goto checkpoint;
          }
          if (max_score == score1b) {
            tokens++;
            i += length;
            length = length1b;
            index = index1b;
            forward_delete = 1;
            goto checkpoint;
          }
          if (max_score == score2b) {
            tokens++;
            i += original.length - forward_delete;
            length = length2b;
            index = index2b;
            forward_delete = 1;
            goto checkpoint;
          }
          if (max_score == score3b) {
            tokens++;
            i += original.length2 - forward_delete;
            length = length3b;
            index = index3b;
            forward_delete = 1;
            goto checkpoint;
          }
        }
      }
      tokens++;
      i += length;
      forward_delete = 0;
    } else {
      if (unk_token_ != does_not_exist) tokens++;
      ++i;
      ++missing;
      forward_delete = 0;
    }
  }
  return {tokens, missing};
}


std::pair<std::vector<std::uint8_t>, int> Vocab::tokenize_to_serialized16(
    std::span<const std::uint8_t> normalized) const {
  Bytes data(normalized.begin(), normalized.end());
  const int len_data = static_cast<int>(data.size());
  data.push_back(0);

  int i = 0, i1 = 0, i2 = 0, i3 = 0;
  int length = 0, length1 = 0, length2 = 0, length3 = 0;
  int length1b = 0, length2b = 0, length3b = 0;
  std::uint32_t index = 0, index1 = 0, index2 = 0, index3 = 0;
  std::uint32_t index1b = 0, index2b = 0, index3b = 0;
  int branch_length = 0, n_words = 0;
  bool found = false, found1 = false, found2 = false, found3 = false;
  int score1 = 0, score2 = 0, score3 = 0, score1b = 0, score2b = 0, score3b = 0;
  int max_score = 0;
  int forward_delete = 0;
  std::uint8_t next_byte = 0;
  TokenOuter original;
  TokenInner first, second;
  length = (len_data / 2) + 4;
  Bytes buffer;
  buffer.reserve(static_cast<std::size_t>(length));
  int missing = 0;

  Bytes lilbuf(static_cast<std::size_t>(max_token_length_), 0);
  if (!lilbuf.empty()) lilbuf[0] = 32;
  int lilbuf_offset = charset_ == 2 ? 2 : 1;
  int max_token_length_with_space = max_token_length_ - lilbuf_offset;

  while (i < len_data) {
    auto longest = dictionary_->longest_substring(
        std::span<const std::uint8_t>(data).subspan(
            static_cast<std::size_t>(i),
            static_cast<std::size_t>(min_int(len_data - i, max_token_length_))));
    index = longest.index;
    length = longest.length;
    found = longest.found;
    if (found) {
    checkpoint:
      original = info_[index].alt;
      i1 = i + length;
      if (i1 < len_data && ((original.data.flag & 32) == 0 || begin_byte_[data[i1]] != 12)) {
        score1 = score2 = score3 = score1b = score2b = score3b = -1000000;
        max_score = -1000000;

        auto l1 = dictionary_->longest_substring(
            std::span<const std::uint8_t>(data).subspan(
                static_cast<std::size_t>(i1),
                static_cast<std::size_t>(min_int(len_data - i1, max_token_length_))));
        index1 = l1.index;
        length1 = l1.length;
        found1 = l1.found;

        if (found1) {
          n_words = int(original.data.n_words) - forward_delete;
          second = info_[index1].alt.data;
          next_byte = begin_byte_[data[i1 + length1]];
          score1 = ((length + length1 + int((original.data.flag >> 7) + (second.flag >> 7)) +
                     max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                     int((second.flag >> 2) & 1) + int((next_byte >> 2) & 1) +
                     ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                    ((int(original.data.flag & 1 & (second.flag >> 1)) * 103) +
                     (int((original.data.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                     (int(second.flag & 1 & next_byte) * 3)));
          max_score = score1;

          if (delete_token_ != does_not_exist && (second.flag & 2) != 0 && next_byte == 1 &&
              second.n_words == 0) {
            length1b = min_int(len_data - i1, max_token_length_with_space);
            std::fill(lilbuf.begin() + lilbuf_offset, lilbuf.end(), 0);
            std::copy_n(data.begin() + i1, length1b, lilbuf.begin() + lilbuf_offset);
            auto l1b = dictionary_->longest_substring(std::span<const std::uint8_t>(
                lilbuf.data(), static_cast<std::size_t>(length1b + lilbuf_offset)));
            index1b = l1b.index;
            length1b = l1b.length;
            if (length1b > length1 + 1) {
              length1b -= lilbuf_offset;
              second = info_[index1b].alt.data;
              next_byte = begin_byte_[data[i1 + length1b]];
              score1b = ((length + length1b + int((original.data.flag >> 7) + (second.flag >> 7)) +
                          max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                          int((next_byte >> 2) & 1) +
                          ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                         ((int(original.data.flag & 1) * 103) +
                          (int((original.data.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                          (int(second.flag & 1 & next_byte) * 3) + 1));
              max_score = max_int(max_score, score1b);
            }
          }
        }

        if (original.index != does_not_exist) {
          i2 = i + original.length - forward_delete;
          auto l2 = dictionary_->longest_substring(
              std::span<const std::uint8_t>(data).subspan(
                  static_cast<std::size_t>(i2),
                  static_cast<std::size_t>(min_int(len_data - i2, max_token_length_))));
          index2 = l2.index;
          length2 = l2.length;
          found2 = l2.found;

          if (found2) {
            first = info_[original.index].alt.data;
            n_words = int(first.n_words) - forward_delete;
            second = info_[index2].alt.data;
            next_byte = begin_byte_[data[i2 + length2]];
            branch_length = original.length + length2 - forward_delete;
            score2 = ((branch_length + int((first.flag >> 7) + (second.flag >> 7)) +
                       max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                       int((second.flag >> 2) & 1) + int((next_byte >> 2) & 1) +
                       ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                      ((int(first.flag & 1 & (second.flag >> 1)) * 103) +
                       (int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                       (int(second.flag & 1 & next_byte) * 3) +
                       (less_than(branch_length, length) * 100) +
                       (equal_to(branch_length, length) * 10000)));
            max_score = max_int(max_score, score2);

            if (delete_token_ != does_not_exist && (second.flag & 2) != 0 && next_byte == 1 &&
                second.n_words == 0) {
              length2b = min_int(len_data - i2, max_token_length_with_space);
              std::fill(lilbuf.begin() + lilbuf_offset, lilbuf.end(), 0);
              std::copy_n(data.begin() + i2, length2b, lilbuf.begin() + lilbuf_offset);
              auto l2b = dictionary_->longest_substring(std::span<const std::uint8_t>(
                  lilbuf.data(), static_cast<std::size_t>(length2b + lilbuf_offset)));
              index2b = l2b.index;
              length2b = l2b.length;
              if (length2b > length2 + 1) {
                length2b -= lilbuf_offset;
                second = info_[index2b].alt.data;
                branch_length = original.length + length2b - forward_delete;
                next_byte = begin_byte_[data[i2 + length2b]];
                score2b =
                    ((branch_length + int((first.flag >> 7) + (second.flag >> 7)) +
                      max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                      int((next_byte >> 2) & 1) +
                      ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                     ((int(first.flag & 1) * 103) +
                      (int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                      (int(second.flag & 1 & next_byte) * 3) + 1 +
                      (less_than(branch_length, length) * 100) +
                      (equal_to(branch_length, length) * 10000)));
                max_score = max_int(max_score, score2b);
              }
            }
          }

          if (original.index2 != does_not_exist) {
            i3 = i + original.length2 - forward_delete;
            auto l3 = dictionary_->longest_substring(
                std::span<const std::uint8_t>(data).subspan(
                    static_cast<std::size_t>(i3),
                    static_cast<std::size_t>(min_int(len_data - i3, max_token_length_))));
            index3 = l3.index;
            length3 = l3.length;
            found3 = l3.found;

            if (found3) {
              first = info_[original.index2].alt.data;
              n_words = int(first.n_words) - forward_delete;
              second = info_[index3].alt.data;
              next_byte = begin_byte_[data[i3 + length3]];
              branch_length = original.length2 + length3 - forward_delete;
              score3 = ((branch_length + int((first.flag >> 7) + (second.flag >> 7)) +
                         max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                         int((second.flag >> 2) & 1) + int((next_byte >> 2) & 1) +
                         ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                        ((int(first.flag & 1 & (second.flag >> 1)) * 103) +
                         (int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                         (int(second.flag & 1 & next_byte) * 3) +
                         (less_than(branch_length, length) * 100) +
                         (equal_to(branch_length, length) * 10000)));
              max_score = max_int(max_score, score3);

              if (delete_token_ != does_not_exist && (second.flag & 2) != 0 && next_byte == 1 &&
                  second.n_words == 0) {
                length3b = min_int(len_data - i3, max_token_length_with_space);
                std::fill(lilbuf.begin() + lilbuf_offset, lilbuf.end(), 0);
                std::copy_n(data.begin() + i3, length3b, lilbuf.begin() + lilbuf_offset);
                auto l3b = dictionary_->longest_substring(std::span<const std::uint8_t>(
                    lilbuf.data(), static_cast<std::size_t>(length3b + lilbuf_offset)));
                index3b = l3b.index;
                length3b = l3b.length;
                if (length3b > length3 + 1) {
                  length3b -= lilbuf_offset;
                  second = info_[index3b].alt.data;
                  branch_length = original.length2 + length3b - forward_delete;
                  next_byte = begin_byte_[data[i3 + length3b]];
                  score3b =
                      ((branch_length + int((first.flag >> 7) + (second.flag >> 7)) +
                        max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                        int((next_byte >> 2) & 1) +
                        ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                       ((int(first.flag & 1) * 103) +
                        (int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                        (int(second.flag & 1 & next_byte) * 3) + 1 +
                        (less_than(branch_length, length) * 100) +
                        (equal_to(branch_length, length) * 10000)));
                  max_score = max_int(max_score, score3b);
                }
              }
            }
          }
        }

        if (max_score != -1000000) {
          if (max_score == score1) {
            buffer.push_back(static_cast<std::uint8_t>(original.id));
            buffer.push_back(static_cast<std::uint8_t>(original.id >> 8));
            i += length;
            length = length1;
            index = index1;
            forward_delete = 0;
            goto checkpoint;
          }
          if (max_score == score2) {
            buffer.push_back(static_cast<std::uint8_t>(original.id1));
            buffer.push_back(static_cast<std::uint8_t>(original.id1 >> 8));
            i += original.length - forward_delete;
            length = length2;
            index = index2;
            forward_delete = 0;
            goto checkpoint;
          }
          if (max_score == score3) {
            buffer.push_back(static_cast<std::uint8_t>(original.id2));
            buffer.push_back(static_cast<std::uint8_t>(original.id2 >> 8));
            i += original.length2 - forward_delete;
            length = length3;
            index = index3;
            forward_delete = 0;
            goto checkpoint;
          }
          if (max_score == score1b) {
            buffer.push_back(static_cast<std::uint8_t>(original.id));
            buffer.push_back(static_cast<std::uint8_t>(original.id >> 8));
            buffer.push_back(static_cast<std::uint8_t>(delete_token_));
            buffer.push_back(static_cast<std::uint8_t>(delete_token_ >> 8));
            i += length;
            length = length1b;
            index = index1b;
            forward_delete = 1;
            goto checkpoint;
          }
          if (max_score == score2b) {
            buffer.push_back(static_cast<std::uint8_t>(original.id1));
            buffer.push_back(static_cast<std::uint8_t>(original.id1 >> 8));
            buffer.push_back(static_cast<std::uint8_t>(delete_token_));
            buffer.push_back(static_cast<std::uint8_t>(delete_token_ >> 8));
            i += original.length - forward_delete;
            length = length2b;
            index = index2b;
            forward_delete = 1;
            goto checkpoint;
          }
          if (max_score == score3b) {
            buffer.push_back(static_cast<std::uint8_t>(original.id2));
            buffer.push_back(static_cast<std::uint8_t>(original.id2 >> 8));
            buffer.push_back(static_cast<std::uint8_t>(delete_token_));
            buffer.push_back(static_cast<std::uint8_t>(delete_token_ >> 8));
            i += original.length2 - forward_delete;
            length = length3b;
            index = index3b;
            forward_delete = 1;
            goto checkpoint;
          }
        }
      }
      buffer.push_back(static_cast<std::uint8_t>(original.id));
            buffer.push_back(static_cast<std::uint8_t>(original.id >> 8));
      i += length;
      forward_delete = 0;
    } else {
      if (unk_token_ != does_not_exist) {
        buffer.push_back(static_cast<std::uint8_t>(unk_token_));
        buffer.push_back(static_cast<std::uint8_t>(unk_token_ >> 8));
      }
      ++i;
      ++missing;
      forward_delete = 0;
    }
  }
  return {std::move(buffer), missing};
}


std::pair<std::vector<std::uint8_t>, int> Vocab::tokenize_to_serialized24(
    std::span<const std::uint8_t> normalized) const {
  Bytes data(normalized.begin(), normalized.end());
  const int len_data = static_cast<int>(data.size());
  data.push_back(0);

  int i = 0, i1 = 0, i2 = 0, i3 = 0;
  int length = 0, length1 = 0, length2 = 0, length3 = 0;
  int length1b = 0, length2b = 0, length3b = 0;
  std::uint32_t index = 0, index1 = 0, index2 = 0, index3 = 0;
  std::uint32_t index1b = 0, index2b = 0, index3b = 0;
  int branch_length = 0, n_words = 0;
  bool found = false, found1 = false, found2 = false, found3 = false;
  int score1 = 0, score2 = 0, score3 = 0, score1b = 0, score2b = 0, score3b = 0;
  int max_score = 0;
  int forward_delete = 0;
  std::uint8_t next_byte = 0;
  TokenOuter original;
  TokenInner first, second;
  length = (len_data / 2) + 6;
  Bytes buffer;
  buffer.reserve(static_cast<std::size_t>(length));
  int missing = 0;

  Bytes lilbuf(static_cast<std::size_t>(max_token_length_), 0);
  if (!lilbuf.empty()) lilbuf[0] = 32;
  int lilbuf_offset = charset_ == 2 ? 2 : 1;
  int max_token_length_with_space = max_token_length_ - lilbuf_offset;

  while (i < len_data) {
    auto longest = dictionary_->longest_substring(
        std::span<const std::uint8_t>(data).subspan(
            static_cast<std::size_t>(i),
            static_cast<std::size_t>(min_int(len_data - i, max_token_length_))));
    index = longest.index;
    length = longest.length;
    found = longest.found;
    if (found) {
    checkpoint:
      original = info_[index].alt;
      i1 = i + length;
      if (i1 < len_data && ((original.data.flag & 32) == 0 || begin_byte_[data[i1]] != 12)) {
        score1 = score2 = score3 = score1b = score2b = score3b = -1000000;
        max_score = -1000000;

        auto l1 = dictionary_->longest_substring(
            std::span<const std::uint8_t>(data).subspan(
                static_cast<std::size_t>(i1),
                static_cast<std::size_t>(min_int(len_data - i1, max_token_length_))));
        index1 = l1.index;
        length1 = l1.length;
        found1 = l1.found;

        if (found1) {
          n_words = int(original.data.n_words) - forward_delete;
          second = info_[index1].alt.data;
          next_byte = begin_byte_[data[i1 + length1]];
          score1 = ((length + length1 + int((original.data.flag >> 7) + (second.flag >> 7)) +
                     max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                     int((second.flag >> 2) & 1) + int((next_byte >> 2) & 1) +
                     ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                    ((int(original.data.flag & 1 & (second.flag >> 1)) * 103) +
                     (int((original.data.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                     (int(second.flag & 1 & next_byte) * 3)));
          max_score = score1;

          if (delete_token_ != does_not_exist && (second.flag & 2) != 0 && next_byte == 1 &&
              second.n_words == 0) {
            length1b = min_int(len_data - i1, max_token_length_with_space);
            std::fill(lilbuf.begin() + lilbuf_offset, lilbuf.end(), 0);
            std::copy_n(data.begin() + i1, length1b, lilbuf.begin() + lilbuf_offset);
            auto l1b = dictionary_->longest_substring(std::span<const std::uint8_t>(
                lilbuf.data(), static_cast<std::size_t>(length1b + lilbuf_offset)));
            index1b = l1b.index;
            length1b = l1b.length;
            if (length1b > length1 + 1) {
              length1b -= lilbuf_offset;
              second = info_[index1b].alt.data;
              next_byte = begin_byte_[data[i1 + length1b]];
              score1b = ((length + length1b + int((original.data.flag >> 7) + (second.flag >> 7)) +
                          max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                          int((next_byte >> 2) & 1) +
                          ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                         ((int(original.data.flag & 1) * 103) +
                          (int((original.data.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                          (int(second.flag & 1 & next_byte) * 3) + 1));
              max_score = max_int(max_score, score1b);
            }
          }
        }

        if (original.index != does_not_exist) {
          i2 = i + original.length - forward_delete;
          auto l2 = dictionary_->longest_substring(
              std::span<const std::uint8_t>(data).subspan(
                  static_cast<std::size_t>(i2),
                  static_cast<std::size_t>(min_int(len_data - i2, max_token_length_))));
          index2 = l2.index;
          length2 = l2.length;
          found2 = l2.found;

          if (found2) {
            first = info_[original.index].alt.data;
            n_words = int(first.n_words) - forward_delete;
            second = info_[index2].alt.data;
            next_byte = begin_byte_[data[i2 + length2]];
            branch_length = original.length + length2 - forward_delete;
            score2 = ((branch_length + int((first.flag >> 7) + (second.flag >> 7)) +
                       max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                       int((second.flag >> 2) & 1) + int((next_byte >> 2) & 1) +
                       ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                      ((int(first.flag & 1 & (second.flag >> 1)) * 103) +
                       (int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                       (int(second.flag & 1 & next_byte) * 3) +
                       (less_than(branch_length, length) * 100) +
                       (equal_to(branch_length, length) * 10000)));
            max_score = max_int(max_score, score2);

            if (delete_token_ != does_not_exist && (second.flag & 2) != 0 && next_byte == 1 &&
                second.n_words == 0) {
              length2b = min_int(len_data - i2, max_token_length_with_space);
              std::fill(lilbuf.begin() + lilbuf_offset, lilbuf.end(), 0);
              std::copy_n(data.begin() + i2, length2b, lilbuf.begin() + lilbuf_offset);
              auto l2b = dictionary_->longest_substring(std::span<const std::uint8_t>(
                  lilbuf.data(), static_cast<std::size_t>(length2b + lilbuf_offset)));
              index2b = l2b.index;
              length2b = l2b.length;
              if (length2b > length2 + 1) {
                length2b -= lilbuf_offset;
                second = info_[index2b].alt.data;
                branch_length = original.length + length2b - forward_delete;
                next_byte = begin_byte_[data[i2 + length2b]];
                score2b =
                    ((branch_length + int((first.flag >> 7) + (second.flag >> 7)) +
                      max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                      int((next_byte >> 2) & 1) +
                      ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                     ((int(first.flag & 1) * 103) +
                      (int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                      (int(second.flag & 1 & next_byte) * 3) + 1 +
                      (less_than(branch_length, length) * 100) +
                      (equal_to(branch_length, length) * 10000)));
                max_score = max_int(max_score, score2b);
              }
            }
          }

          if (original.index2 != does_not_exist) {
            i3 = i + original.length2 - forward_delete;
            auto l3 = dictionary_->longest_substring(
                std::span<const std::uint8_t>(data).subspan(
                    static_cast<std::size_t>(i3),
                    static_cast<std::size_t>(min_int(len_data - i3, max_token_length_))));
            index3 = l3.index;
            length3 = l3.length;
            found3 = l3.found;

            if (found3) {
              first = info_[original.index2].alt.data;
              n_words = int(first.n_words) - forward_delete;
              second = info_[index3].alt.data;
              next_byte = begin_byte_[data[i3 + length3]];
              branch_length = original.length2 + length3 - forward_delete;
              score3 = ((branch_length + int((first.flag >> 7) + (second.flag >> 7)) +
                         max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                         int((second.flag >> 2) & 1) + int((next_byte >> 2) & 1) +
                         ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                        ((int(first.flag & 1 & (second.flag >> 1)) * 103) +
                         (int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                         (int(second.flag & 1 & next_byte) * 3) +
                         (less_than(branch_length, length) * 100) +
                         (equal_to(branch_length, length) * 10000)));
              max_score = max_int(max_score, score3);

              if (delete_token_ != does_not_exist && (second.flag & 2) != 0 && next_byte == 1 &&
                  second.n_words == 0) {
                length3b = min_int(len_data - i3, max_token_length_with_space);
                std::fill(lilbuf.begin() + lilbuf_offset, lilbuf.end(), 0);
                std::copy_n(data.begin() + i3, length3b, lilbuf.begin() + lilbuf_offset);
                auto l3b = dictionary_->longest_substring(std::span<const std::uint8_t>(
                    lilbuf.data(), static_cast<std::size_t>(length3b + lilbuf_offset)));
                index3b = l3b.index;
                length3b = l3b.length;
                if (length3b > length3 + 1) {
                  length3b -= lilbuf_offset;
                  second = info_[index3b].alt.data;
                  branch_length = original.length2 + length3b - forward_delete;
                  next_byte = begin_byte_[data[i3 + length3b]];
                  score3b =
                      ((branch_length + int((first.flag >> 7) + (second.flag >> 7)) +
                        max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                        int((next_byte >> 2) & 1) +
                        ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                       ((int(first.flag & 1) * 103) +
                        (int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                        (int(second.flag & 1 & next_byte) * 3) + 1 +
                        (less_than(branch_length, length) * 100) +
                        (equal_to(branch_length, length) * 10000)));
                  max_score = max_int(max_score, score3b);
                }
              }
            }
          }
        }

        if (max_score != -1000000) {
          if (max_score == score1) {
            buffer.push_back(static_cast<std::uint8_t>(original.id));
            buffer.push_back(static_cast<std::uint8_t>(original.id >> 8));
            buffer.push_back(static_cast<std::uint8_t>(original.id >> 16));
            i += length;
            length = length1;
            index = index1;
            forward_delete = 0;
            goto checkpoint;
          }
          if (max_score == score2) {
            buffer.push_back(static_cast<std::uint8_t>(original.id1));
            buffer.push_back(static_cast<std::uint8_t>(original.id1 >> 8));
            buffer.push_back(static_cast<std::uint8_t>(original.id1 >> 16));
            i += original.length - forward_delete;
            length = length2;
            index = index2;
            forward_delete = 0;
            goto checkpoint;
          }
          if (max_score == score3) {
            buffer.push_back(static_cast<std::uint8_t>(original.id2));
            buffer.push_back(static_cast<std::uint8_t>(original.id2 >> 8));
            buffer.push_back(static_cast<std::uint8_t>(original.id2 >> 16));
            i += original.length2 - forward_delete;
            length = length3;
            index = index3;
            forward_delete = 0;
            goto checkpoint;
          }
          if (max_score == score1b) {
            buffer.push_back(static_cast<std::uint8_t>(original.id));
            buffer.push_back(static_cast<std::uint8_t>(original.id >> 8));
            buffer.push_back(static_cast<std::uint8_t>(original.id >> 16));
            buffer.push_back(static_cast<std::uint8_t>(delete_token_));
            buffer.push_back(static_cast<std::uint8_t>(delete_token_ >> 8));
            buffer.push_back(static_cast<std::uint8_t>(delete_token_ >> 16));
            i += length;
            length = length1b;
            index = index1b;
            forward_delete = 1;
            goto checkpoint;
          }
          if (max_score == score2b) {
            buffer.push_back(static_cast<std::uint8_t>(original.id1));
            buffer.push_back(static_cast<std::uint8_t>(original.id1 >> 8));
            buffer.push_back(static_cast<std::uint8_t>(original.id1 >> 16));
            buffer.push_back(static_cast<std::uint8_t>(delete_token_));
            buffer.push_back(static_cast<std::uint8_t>(delete_token_ >> 8));
            buffer.push_back(static_cast<std::uint8_t>(delete_token_ >> 16));
            i += original.length - forward_delete;
            length = length2b;
            index = index2b;
            forward_delete = 1;
            goto checkpoint;
          }
          if (max_score == score3b) {
            buffer.push_back(static_cast<std::uint8_t>(original.id2));
            buffer.push_back(static_cast<std::uint8_t>(original.id2 >> 8));
            buffer.push_back(static_cast<std::uint8_t>(original.id2 >> 16));
            buffer.push_back(static_cast<std::uint8_t>(delete_token_));
            buffer.push_back(static_cast<std::uint8_t>(delete_token_ >> 8));
            buffer.push_back(static_cast<std::uint8_t>(delete_token_ >> 16));
            i += original.length2 - forward_delete;
            length = length3b;
            index = index3b;
            forward_delete = 1;
            goto checkpoint;
          }
        }
      }
      buffer.push_back(static_cast<std::uint8_t>(original.id));
            buffer.push_back(static_cast<std::uint8_t>(original.id >> 8));
            buffer.push_back(static_cast<std::uint8_t>(original.id >> 16));
      i += length;
      forward_delete = 0;
    } else {
      if (unk_token_ != does_not_exist) {
        buffer.push_back(static_cast<std::uint8_t>(unk_token_));
        buffer.push_back(static_cast<std::uint8_t>(unk_token_ >> 8));
        buffer.push_back(static_cast<std::uint8_t>(unk_token_ >> 16));
      }
      ++i;
      ++missing;
      forward_delete = 0;
    }
  }
  return {std::move(buffer), missing};
}


std::pair<std::vector<std::uint8_t>, int> Vocab::tokenize_to_serialized32(
    std::span<const std::uint8_t> normalized) const {
  Bytes data(normalized.begin(), normalized.end());
  const int len_data = static_cast<int>(data.size());
  data.push_back(0);

  int i = 0, i1 = 0, i2 = 0, i3 = 0;
  int length = 0, length1 = 0, length2 = 0, length3 = 0;
  int length1b = 0, length2b = 0, length3b = 0;
  std::uint32_t index = 0, index1 = 0, index2 = 0, index3 = 0;
  std::uint32_t index1b = 0, index2b = 0, index3b = 0;
  int branch_length = 0, n_words = 0;
  bool found = false, found1 = false, found2 = false, found3 = false;
  int score1 = 0, score2 = 0, score3 = 0, score1b = 0, score2b = 0, score3b = 0;
  int max_score = 0;
  int forward_delete = 0;
  std::uint8_t next_byte = 0;
  TokenOuter original;
  TokenInner first, second;
  length = len_data + 8;
  Bytes buffer;
  buffer.reserve(static_cast<std::size_t>(length));
  int missing = 0;

  Bytes lilbuf(static_cast<std::size_t>(max_token_length_), 0);
  if (!lilbuf.empty()) lilbuf[0] = 32;
  int lilbuf_offset = charset_ == 2 ? 2 : 1;
  int max_token_length_with_space = max_token_length_ - lilbuf_offset;

  while (i < len_data) {
    auto longest = dictionary_->longest_substring(
        std::span<const std::uint8_t>(data).subspan(
            static_cast<std::size_t>(i),
            static_cast<std::size_t>(min_int(len_data - i, max_token_length_))));
    index = longest.index;
    length = longest.length;
    found = longest.found;
    if (found) {
    checkpoint:
      original = info_[index].alt;
      i1 = i + length;
      if (i1 < len_data && ((original.data.flag & 32) == 0 || begin_byte_[data[i1]] != 12)) {
        score1 = score2 = score3 = score1b = score2b = score3b = -1000000;
        max_score = -1000000;

        auto l1 = dictionary_->longest_substring(
            std::span<const std::uint8_t>(data).subspan(
                static_cast<std::size_t>(i1),
                static_cast<std::size_t>(min_int(len_data - i1, max_token_length_))));
        index1 = l1.index;
        length1 = l1.length;
        found1 = l1.found;

        if (found1) {
          n_words = int(original.data.n_words) - forward_delete;
          second = info_[index1].alt.data;
          next_byte = begin_byte_[data[i1 + length1]];
          score1 = ((length + length1 + int((original.data.flag >> 7) + (second.flag >> 7)) +
                     max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                     int((second.flag >> 2) & 1) + int((next_byte >> 2) & 1) +
                     ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                    ((int(original.data.flag & 1 & (second.flag >> 1)) * 103) +
                     (int((original.data.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                     (int(second.flag & 1 & next_byte) * 3)));
          max_score = score1;

          if (delete_token_ != does_not_exist && (second.flag & 2) != 0 && next_byte == 1 &&
              second.n_words == 0) {
            length1b = min_int(len_data - i1, max_token_length_with_space);
            std::fill(lilbuf.begin() + lilbuf_offset, lilbuf.end(), 0);
            std::copy_n(data.begin() + i1, length1b, lilbuf.begin() + lilbuf_offset);
            auto l1b = dictionary_->longest_substring(std::span<const std::uint8_t>(
                lilbuf.data(), static_cast<std::size_t>(length1b + lilbuf_offset)));
            index1b = l1b.index;
            length1b = l1b.length;
            if (length1b > length1 + 1) {
              length1b -= lilbuf_offset;
              second = info_[index1b].alt.data;
              next_byte = begin_byte_[data[i1 + length1b]];
              score1b = ((length + length1b + int((original.data.flag >> 7) + (second.flag >> 7)) +
                          max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                          int((next_byte >> 2) & 1) +
                          ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                         ((int(original.data.flag & 1) * 103) +
                          (int((original.data.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                          (int(second.flag & 1 & next_byte) * 3) + 1));
              max_score = max_int(max_score, score1b);
            }
          }
        }

        if (original.index != does_not_exist) {
          i2 = i + original.length - forward_delete;
          auto l2 = dictionary_->longest_substring(
              std::span<const std::uint8_t>(data).subspan(
                  static_cast<std::size_t>(i2),
                  static_cast<std::size_t>(min_int(len_data - i2, max_token_length_))));
          index2 = l2.index;
          length2 = l2.length;
          found2 = l2.found;

          if (found2) {
            first = info_[original.index].alt.data;
            n_words = int(first.n_words) - forward_delete;
            second = info_[index2].alt.data;
            next_byte = begin_byte_[data[i2 + length2]];
            branch_length = original.length + length2 - forward_delete;
            score2 = ((branch_length + int((first.flag >> 7) + (second.flag >> 7)) +
                       max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                       int((second.flag >> 2) & 1) + int((next_byte >> 2) & 1) +
                       ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                      ((int(first.flag & 1 & (second.flag >> 1)) * 103) +
                       (int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                       (int(second.flag & 1 & next_byte) * 3) +
                       (less_than(branch_length, length) * 100) +
                       (equal_to(branch_length, length) * 10000)));
            max_score = max_int(max_score, score2);

            if (delete_token_ != does_not_exist && (second.flag & 2) != 0 && next_byte == 1 &&
                second.n_words == 0) {
              length2b = min_int(len_data - i2, max_token_length_with_space);
              std::fill(lilbuf.begin() + lilbuf_offset, lilbuf.end(), 0);
              std::copy_n(data.begin() + i2, length2b, lilbuf.begin() + lilbuf_offset);
              auto l2b = dictionary_->longest_substring(std::span<const std::uint8_t>(
                  lilbuf.data(), static_cast<std::size_t>(length2b + lilbuf_offset)));
              index2b = l2b.index;
              length2b = l2b.length;
              if (length2b > length2 + 1) {
                length2b -= lilbuf_offset;
                second = info_[index2b].alt.data;
                branch_length = original.length + length2b - forward_delete;
                next_byte = begin_byte_[data[i2 + length2b]];
                score2b =
                    ((branch_length + int((first.flag >> 7) + (second.flag >> 7)) +
                      max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                      int((next_byte >> 2) & 1) +
                      ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                     ((int(first.flag & 1) * 103) +
                      (int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                      (int(second.flag & 1 & next_byte) * 3) + 1 +
                      (less_than(branch_length, length) * 100) +
                      (equal_to(branch_length, length) * 10000)));
                max_score = max_int(max_score, score2b);
              }
            }
          }

          if (original.index2 != does_not_exist) {
            i3 = i + original.length2 - forward_delete;
            auto l3 = dictionary_->longest_substring(
                std::span<const std::uint8_t>(data).subspan(
                    static_cast<std::size_t>(i3),
                    static_cast<std::size_t>(min_int(len_data - i3, max_token_length_))));
            index3 = l3.index;
            length3 = l3.length;
            found3 = l3.found;

            if (found3) {
              first = info_[original.index2].alt.data;
              n_words = int(first.n_words) - forward_delete;
              second = info_[index3].alt.data;
              next_byte = begin_byte_[data[i3 + length3]];
              branch_length = original.length2 + length3 - forward_delete;
              score3 = ((branch_length + int((first.flag >> 7) + (second.flag >> 7)) +
                         max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                         int((second.flag >> 2) & 1) + int((next_byte >> 2) & 1) +
                         ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                        ((int(first.flag & 1 & (second.flag >> 1)) * 103) +
                         (int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                         (int(second.flag & 1 & next_byte) * 3) +
                         (less_than(branch_length, length) * 100) +
                         (equal_to(branch_length, length) * 10000)));
              max_score = max_int(max_score, score3);

              if (delete_token_ != does_not_exist && (second.flag & 2) != 0 && next_byte == 1 &&
                  second.n_words == 0) {
                length3b = min_int(len_data - i3, max_token_length_with_space);
                std::fill(lilbuf.begin() + lilbuf_offset, lilbuf.end(), 0);
                std::copy_n(data.begin() + i3, length3b, lilbuf.begin() + lilbuf_offset);
                auto l3b = dictionary_->longest_substring(std::span<const std::uint8_t>(
                    lilbuf.data(), static_cast<std::size_t>(length3b + lilbuf_offset)));
                index3b = l3b.index;
                length3b = l3b.length;
                if (length3b > length3 + 1) {
                  length3b -= lilbuf_offset;
                  second = info_[index3b].alt.data;
                  branch_length = original.length2 + length3b - forward_delete;
                  next_byte = begin_byte_[data[i3 + length3b]];
                  score3b =
                      ((branch_length + int((first.flag >> 7) + (second.flag >> 7)) +
                        max_zero_and(n_words - 1) + max_zero_and(int(second.n_words) - 1) +
                        int((next_byte >> 2) & 1) +
                        ((n_words + int(second.n_words + (next_byte >> 3))) * 100)) -
                       ((int(first.flag & 1) * 103) +
                        (int((first.flag >> 3) & 1 & (second.flag >> 4)) * 100) +
                        (int(second.flag & 1 & next_byte) * 3) + 1 +
                        (less_than(branch_length, length) * 100) +
                        (equal_to(branch_length, length) * 10000)));
                  max_score = max_int(max_score, score3b);
                }
              }
            }
          }
        }

        if (max_score != -1000000) {
          if (max_score == score1) {
            buffer.push_back(static_cast<std::uint8_t>(original.id));
            buffer.push_back(static_cast<std::uint8_t>(original.id >> 8));
            buffer.push_back(static_cast<std::uint8_t>(original.id >> 16));
            buffer.push_back(0);
            i += length;
            length = length1;
            index = index1;
            forward_delete = 0;
            goto checkpoint;
          }
          if (max_score == score2) {
            buffer.push_back(static_cast<std::uint8_t>(original.id1));
            buffer.push_back(static_cast<std::uint8_t>(original.id1 >> 8));
            buffer.push_back(static_cast<std::uint8_t>(original.id1 >> 16));
            buffer.push_back(0);
            i += original.length - forward_delete;
            length = length2;
            index = index2;
            forward_delete = 0;
            goto checkpoint;
          }
          if (max_score == score3) {
            buffer.push_back(static_cast<std::uint8_t>(original.id2));
            buffer.push_back(static_cast<std::uint8_t>(original.id2 >> 8));
            buffer.push_back(static_cast<std::uint8_t>(original.id2 >> 16));
            buffer.push_back(0);
            i += original.length2 - forward_delete;
            length = length3;
            index = index3;
            forward_delete = 0;
            goto checkpoint;
          }
          if (max_score == score1b) {
            buffer.push_back(static_cast<std::uint8_t>(original.id));
            buffer.push_back(static_cast<std::uint8_t>(original.id >> 8));
            buffer.push_back(static_cast<std::uint8_t>(original.id >> 16));
            buffer.push_back(0);
            buffer.push_back(static_cast<std::uint8_t>(delete_token_));
            buffer.push_back(static_cast<std::uint8_t>(delete_token_ >> 8));
            buffer.push_back(static_cast<std::uint8_t>(delete_token_ >> 16));
            buffer.push_back(0);
            i += length;
            length = length1b;
            index = index1b;
            forward_delete = 1;
            goto checkpoint;
          }
          if (max_score == score2b) {
            buffer.push_back(static_cast<std::uint8_t>(original.id1));
            buffer.push_back(static_cast<std::uint8_t>(original.id1 >> 8));
            buffer.push_back(static_cast<std::uint8_t>(original.id1 >> 16));
            buffer.push_back(0);
            buffer.push_back(static_cast<std::uint8_t>(delete_token_));
            buffer.push_back(static_cast<std::uint8_t>(delete_token_ >> 8));
            buffer.push_back(static_cast<std::uint8_t>(delete_token_ >> 16));
            buffer.push_back(0);
            i += original.length - forward_delete;
            length = length2b;
            index = index2b;
            forward_delete = 1;
            goto checkpoint;
          }
          if (max_score == score3b) {
            buffer.push_back(static_cast<std::uint8_t>(original.id2));
            buffer.push_back(static_cast<std::uint8_t>(original.id2 >> 8));
            buffer.push_back(static_cast<std::uint8_t>(original.id2 >> 16));
            buffer.push_back(0);
            buffer.push_back(static_cast<std::uint8_t>(delete_token_));
            buffer.push_back(static_cast<std::uint8_t>(delete_token_ >> 8));
            buffer.push_back(static_cast<std::uint8_t>(delete_token_ >> 16));
            buffer.push_back(0);
            i += original.length2 - forward_delete;
            length = length3b;
            index = index3b;
            forward_delete = 1;
            goto checkpoint;
          }
        }
      }
      buffer.push_back(static_cast<std::uint8_t>(original.id));
            buffer.push_back(static_cast<std::uint8_t>(original.id >> 8));
            buffer.push_back(static_cast<std::uint8_t>(original.id >> 16));
            buffer.push_back(0);
      i += length;
      forward_delete = 0;
    } else {
      if (unk_token_ != does_not_exist) {
        buffer.push_back(static_cast<std::uint8_t>(unk_token_));
        buffer.push_back(static_cast<std::uint8_t>(unk_token_ >> 8));
        buffer.push_back(static_cast<std::uint8_t>(unk_token_ >> 16));
        buffer.push_back(0);
      }
      ++i;
      ++missing;
      forward_delete = 0;
    }
  }
  return {std::move(buffer), missing};
}


TokenizeResult Vocab::tokenize(std::span<const std::uint8_t> data) const {
  if (max_token_length_ == 0) return {};
  auto normalized = normalize(data);
  return tokenize_normalized(normalized);
}

CountResult Vocab::count(std::span<const std::uint8_t> data) const {
  if (max_token_length_ == 0) return {};
  auto normalized = normalize(data);
  return tokenize_count_normalized(normalized);
}

SerializedResult Vocab::tokenize_serialized(std::span<const std::uint8_t> data,
                                            std::uint8_t encoding_length) const {
  if (max_token_length_ == 0) return {{}, 2, 0};
  if (encoding_length <= 1) {
    encoding_length = reverse_.size() <= 65536 ? 2 : 3;
  }
  auto normalized = normalize(data);
  if (encoding_length == 2) {
    auto [bytes, missing] = tokenize_to_serialized16(normalized);
    return {std::move(bytes), 2, missing};
  }
  if (encoding_length == 3) {
    auto [bytes, missing] = tokenize_to_serialized24(normalized);
    return {std::move(bytes), 3, missing};
  }
  if (encoding_length == 4) {
    auto [bytes, missing] = tokenize_to_serialized32(normalized);
    return {std::move(bytes), 4, missing};
  }
  throw Error("invalid encoding length");
}

std::vector<Info> Vocab::tokens_detailed() const {
  std::vector<Info> infos(static_cast<std::size_t>(vocab_size_));
  std::size_t on = 0;
  for (const auto& tok : info_) {
    if (tok.score < -0.5F) continue;
    if (on >= infos.size()) infos.resize(on + 1);
    infos[on].id = tok.alt.id;
    infos[on].token = tok.token;
    infos[on].token_decoded = denormalize(tok.token);
    infos[on].type = 0;
    if (tok.token.size() == 1) {
      infos[on].type = 1;
    } else if ((tok.alt.data.flag & 64) != 0) {
      infos[on].type = 2;
    }
    infos[on].score = tok.score;
    ++on;
  }
  if (unk_token_ != does_not_exist) {
    if (on >= infos.size()) infos.resize(on + 1);
    infos[on].id = unk_token_;
    infos[on].type = 3;
  }
  return infos;
}

std::vector<Info> Vocab::special_tokens() const {
  std::vector<Info> list;
  for (const auto& tok : info_) {
    if ((tok.alt.data.flag & 64) != 0 && tok.score >= -0.5F) {
      Info info;
      info.id = tok.alt.id;
      info.type = 2;
      info.token = tok.token;
      info.token_decoded = denormalize(tok.token);
      info.score = tok.score;
      list.push_back(std::move(info));
    }
  }
  return list;
}

std::vector<std::vector<std::uint8_t>> Vocab::tokens() const {
  std::vector<Bytes> list;
  list.reserve(static_cast<std::size_t>(vocab_size_));
  for (const auto& tok : info_) {
    if (tok.score > -0.5F) list.push_back(tok.token);
  }
  return list;
}

std::optional<std::vector<std::uint8_t>> Vocab::id_to_token(std::uint32_t id) const {
  if (id >= reverse_.size()) return std::nullopt;
  return reverse_[id];
}

std::optional<std::uint32_t> Vocab::token_to_id(std::span<const std::uint8_t> token) const {
  auto [index, found] = dictionary_->find(token);
  if (!found) return std::nullopt;
  return info_[index].alt.id;
}

std::vector<std::uint8_t> Vocab::denormalize(std::span<const std::uint8_t> token) const {
  Bytes data(token.begin(), token.end());
  if (using_capcode_ == 2) return capcode::decode(std::move(data));
  if (using_capcode_ == 1) return capcode::no_capcode_decode(std::move(data));
  return data;
}

int Vocab::highest_token_id() const { return static_cast<int>(reverse_.size()) - 1; }

}  // namespace tokenmonster
