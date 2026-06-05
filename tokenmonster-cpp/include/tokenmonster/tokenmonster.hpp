#pragma once

#include <cstddef>
#include <cstdint>
#include <filesystem>
#include <memory>
#include <optional>
#include <span>
#include <stdexcept>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

#include <capcode/capcode.hpp>

namespace tokenmonster {

constexpr std::uint32_t does_not_exist = 16777215U;

class Error : public std::runtime_error {
 public:
  using std::runtime_error::runtime_error;
};

struct TokenizeResult {
  std::vector<std::uint32_t> tokens;
  int missing = 0;
};

struct CountResult {
  int tokens = 0;
  int missing = 0;
};

struct SerializedResult {
  std::vector<std::uint8_t> bytes;
  std::uint8_t encoding_length = 0;
  int missing = 0;
};

struct Info {
  std::uint32_t id = 0;
  std::vector<std::uint8_t> token;
  std::vector<std::uint8_t> token_decoded;
  std::uint8_t type = 0;
  float score = 0.0F;
};

class Vocab;

class Decoder {
 public:
  Decoder() = default;

  std::vector<std::uint8_t> decode(std::span<const std::uint32_t> tokens);
  std::vector<std::uint8_t> decode_serialized(std::span<const std::uint8_t> data,
                                              std::uint8_t encoding_length = 0);
  std::vector<std::uint32_t> deserialize(std::span<const std::uint8_t> data,
                                         std::uint8_t encoding_length = 0) const;
  std::vector<std::uint8_t> flush();

 private:
  friend class Vocab;
  explicit Decoder(const Vocab* vocab) : vocab_(vocab) {}

  const Vocab* vocab_ = nullptr;
  std::vector<std::uint8_t> remainder_;
  capcode::Decoder capcode_decoder_;
};

class Vocab {
 public:
  Vocab();
  ~Vocab();
  Vocab(Vocab&&) noexcept;
  Vocab& operator=(Vocab&&) noexcept;
  Vocab(const Vocab&) = delete;
  Vocab& operator=(const Vocab&) = delete;

  static Vocab load(const std::filesystem::path& path);

  std::vector<std::uint8_t> normalize(std::span<const std::uint8_t> data) const;
  TokenizeResult tokenize(std::span<const std::uint8_t> data) const;
  CountResult count(std::span<const std::uint8_t> data) const;
  SerializedResult tokenize_serialized(std::span<const std::uint8_t> data,
                                       std::uint8_t encoding_length = 0) const;

  std::vector<std::uint8_t> decode(std::span<const std::uint32_t> tokens) const;
  std::vector<std::uint8_t> decode_serialized(std::span<const std::uint8_t> data,
                                              std::uint8_t encoding_length = 0) const;
  std::vector<std::uint32_t> deserialize(std::span<const std::uint8_t> data,
                                         std::uint8_t encoding_length = 0) const;

  Decoder new_decoder() const { return Decoder(this); }

  std::vector<Info> tokens_detailed() const;
  std::vector<Info> special_tokens() const;
  std::vector<std::vector<std::uint8_t>> tokens() const;
  std::optional<std::vector<std::uint8_t>> id_to_token(std::uint32_t id) const;
  std::optional<std::uint32_t> token_to_id(std::span<const std::uint8_t> token) const;
  std::vector<std::uint8_t> denormalize(std::span<const std::uint8_t> token) const;

  std::uint32_t unk() const { return unk_token_; }
  bool has_unk() const { return unk_token_ != does_not_exist; }
  int size() const { return vocab_size_; }
  int max_token_length() const { return max_token_length_; }
  std::uint8_t charset() const { return charset_; }
  std::uint8_t capcode() const { return using_capcode_; }
  std::uint8_t mode() const { return level_; }
  std::uint8_t normalization_code() const { return normalizer_flag_; }
  int highest_token_id() const;

 private:
  friend class Decoder;

  struct TokenInner {
    std::uint8_t flag = 0;
    std::uint8_t n_words = 0;
  };

  struct TokenOuter {
    TokenInner data;
    int length = 0;
    int length2 = 0;
    std::uint32_t index = does_not_exist;
    std::uint32_t index2 = does_not_exist;
    std::uint32_t id = 0;
    std::uint32_t id1 = 0;
    std::uint32_t id2 = 0;
  };

  struct TokenInfo {
    TokenOuter alt;
    std::vector<std::uint8_t> token;
    float score = 0.0F;
  };

  struct DeletedToken {
    std::vector<std::uint8_t> token;
    std::uint32_t id = does_not_exist;
    float score = 0.0F;
  };

  class FastDictionary;

  TokenizeResult tokenize_normalized(std::span<const std::uint8_t> normalized) const;
  CountResult tokenize_count_normalized(std::span<const std::uint8_t> normalized) const;
  std::pair<std::vector<std::uint8_t>, int> tokenize_to_serialized16(
      std::span<const std::uint8_t> normalized) const;
  std::pair<std::vector<std::uint8_t>, int> tokenize_to_serialized24(
      std::span<const std::uint8_t> normalized) const;
  std::pair<std::vector<std::uint8_t>, int> tokenize_to_serialized32(
      std::span<const std::uint8_t> normalized) const;
  std::vector<std::uint8_t> decode_raw(std::span<const std::uint32_t> tokens) const;
  std::vector<std::uint8_t> decode_serialized_raw(std::span<const std::uint8_t> data,
                                                  std::uint8_t encoding_length) const;

  std::unique_ptr<FastDictionary> dictionary_;
  std::vector<TokenInfo> info_;
  std::vector<std::vector<std::uint8_t>> reverse_;
  std::vector<DeletedToken> deleted_;
  std::uint8_t begin_byte_[256]{};
  int vocab_size_ = 0;
  int max_token_length_ = 0;
  std::uint32_t delete_token_ = does_not_exist;
  std::uint32_t unk_token_ = does_not_exist;
  std::uint8_t using_capcode_ = 0;
  std::uint8_t charset_ = 0;
  std::uint8_t level_ = 0;
  std::uint8_t reserve_ = 0;
  std::uint8_t normalizer_flag_ = 0;
};

}  // namespace tokenmonster
