#include "openloader-access-control.h"

#include "json.h"
#include "hash/hash.h"

#include <algorithm>
#include <array>
#include <cctype>
#include <cstdint>
#include <fstream>
#include <iterator>
#include <stdexcept>

#ifdef _WIN32
#  include <winsock2.h>
#  include <ws2tcpip.h>
#else
#  include <arpa/inet.h>
#endif

namespace {

constexpr auto refresh_interval = std::chrono::seconds(10);

struct parsed_ip {
    int family = 0;
    std::array<uint8_t, 16> bytes {};
    size_t size = 0;
};

std::string trim(std::string value) {
    const auto first = std::find_if_not(value.begin(), value.end(), [](unsigned char c) { return std::isspace(c); });
    const auto last = std::find_if_not(value.rbegin(), value.rend(), [](unsigned char c) { return std::isspace(c); }).base();
    if (first >= last) {
        return {};
    }
    return std::string(first, last);
}

std::string lowercase(std::string value) {
    std::transform(value.begin(), value.end(), value.begin(), [](unsigned char c) {
        return static_cast<char>(std::tolower(c));
    });
    return value;
}

std::string address_without_zone(std::string value) {
    value = trim(std::move(value));
    if (value.size() >= 2 && value.front() == '[' && value.back() == ']') {
        value = value.substr(1, value.size() - 2);
    }
    if (const auto zone = value.rfind('%'); zone != std::string::npos) {
        value.resize(zone);
    }
    return value;
}

bool parse_ip_address(const std::string & input, parsed_ip & output) {
    const std::string value = address_without_zone(input);
    in_addr address4 {};
    if (inet_pton(AF_INET, value.c_str(), &address4) == 1) {
        output.family = AF_INET;
        output.size = 4;
        std::copy_n(reinterpret_cast<const uint8_t *>(&address4), output.size, output.bytes.begin());
        return true;
    }

    in6_addr address6 {};
    if (inet_pton(AF_INET6, value.c_str(), &address6) != 1) {
        return false;
    }
    const auto * bytes = reinterpret_cast<const uint8_t *>(&address6);
    const bool mapped_ipv4 = std::all_of(bytes, bytes + 10, [](uint8_t byte) { return byte == 0; })
        && bytes[10] == 0xff && bytes[11] == 0xff;
    if (mapped_ipv4) {
        output.family = AF_INET;
        output.size = 4;
        std::copy_n(bytes + 12, output.size, output.bytes.begin());
    } else {
        output.family = AF_INET6;
        output.size = 16;
        std::copy_n(bytes, output.size, output.bytes.begin());
    }
    return true;
}

bool same_ip(const parsed_ip & lhs, const parsed_ip & rhs) {
    return lhs.family == rhs.family
        && lhs.size == rhs.size
        && std::equal(lhs.bytes.begin(), lhs.bytes.begin() + lhs.size, rhs.bytes.begin());
}

bool matches_cidr(const parsed_ip & remote, const std::string & pattern) {
    const auto slash = pattern.find('/');
    if (slash == std::string::npos) {
        return false;
    }
    parsed_ip network;
    if (!parse_ip_address(pattern.substr(0, slash), network) || network.family != remote.family) {
        return false;
    }
    const std::string prefix_text = pattern.substr(slash + 1);
    if (prefix_text.empty() || !std::all_of(prefix_text.begin(), prefix_text.end(), [](unsigned char c) { return std::isdigit(c); })) {
        return false;
    }
    const int prefix = std::stoi(prefix_text);
    const int maximum = static_cast<int>(remote.size * 8);
    if (prefix < 0 || prefix > maximum) {
        return false;
    }
    const size_t full_bytes = static_cast<size_t>(prefix / 8);
    const int remaining_bits = prefix % 8;
    if (!std::equal(remote.bytes.begin(), remote.bytes.begin() + full_bytes, network.bytes.begin())) {
        return false;
    }
    if (remaining_bits == 0) {
        return true;
    }
    const uint8_t mask = static_cast<uint8_t>(0xff << (8 - remaining_bits));
    return (remote.bytes[full_bytes] & mask) == (network.bytes[full_bytes] & mask);
}

bool wildcard_match(const std::string & value, const std::string & pattern) {
    size_t value_index = 0;
    size_t pattern_index = 0;
    size_t wildcard_index = std::string::npos;
    size_t wildcard_value_index = 0;
    while (value_index < value.size()) {
        if (pattern_index < pattern.size() && pattern[pattern_index] == value[value_index]) {
            ++value_index;
            ++pattern_index;
        } else if (pattern_index < pattern.size() && pattern[pattern_index] == '*') {
            wildcard_index = pattern_index++;
            wildcard_value_index = value_index;
        } else if (wildcard_index != std::string::npos) {
            pattern_index = wildcard_index + 1;
            value_index = ++wildcard_value_index;
        } else {
            return false;
        }
    }
    while (pattern_index < pattern.size() && pattern[pattern_index] == '*') {
        ++pattern_index;
    }
    return pattern_index == pattern.size();
}

bool is_valid_hash(const std::string & value) {
    return value.size() == 64 && std::all_of(value.begin(), value.end(), [](unsigned char c) {
        return std::isdigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F');
    });
}

bool is_valid_wildcard_pattern(const std::string & value) {
    return !value.empty() && value.size() <= 128
        && std::all_of(value.begin(), value.end(), [](unsigned char c) {
            return std::isdigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
                || c == ':' || c == '.' || c == '*';
        });
}

bool validate_pattern(const std::string & value) {
    if (value == "*") {
        return true;
    }
    if (value.find('/') != std::string::npos) {
        parsed_ip unused;
        const auto slash = value.find('/');
        if (!parse_ip_address(value.substr(0, slash), unused)) {
            return false;
        }
        const std::string prefix_text = value.substr(slash + 1);
        if (prefix_text.empty() || !std::all_of(prefix_text.begin(), prefix_text.end(), [](unsigned char c) { return std::isdigit(c); })) {
            return false;
        }
        const int prefix = std::stoi(prefix_text);
        return prefix >= 0 && prefix <= static_cast<int>(unused.size * 8);
    }
    if (value.find('*') != std::string::npos) {
        return is_valid_wildcard_pattern(value);
    }
    parsed_ip unused;
    return parse_ip_address(value, unused);
}

bool constant_time_equal(const std::string & lhs, const std::string & rhs) {
    if (lhs.size() != rhs.size()) {
        return false;
    }
    unsigned int difference = 0;
    for (size_t index = 0; index < lhs.size(); ++index) {
        difference |= static_cast<unsigned char>(lhs[index]) ^ static_cast<unsigned char>(rhs[index]);
    }
    return difference == 0;
}

bool key_allowed(const std::string & key, const std::vector<std::string> & hashes) {
    const std::string digest = hash_sha256_hex(key.data(), key.size());
    bool matched = false;
    for (const std::string & expected : hashes) {
        matched = constant_time_equal(digest, expected) || matched;
    }
    return matched;
}

bool ip_allowed(const std::string & remote_address, const std::vector<std::string> & patterns) {
    parsed_ip remote;
    if (!parse_ip_address(remote_address, remote)) {
        return false;
    }
    const std::string comparable = lowercase(address_without_zone(remote_address));
    for (const std::string & raw_pattern : patterns) {
        const std::string pattern = lowercase(raw_pattern);
        if (pattern == "*") {
            return true;
        }
        if (pattern.find('/') != std::string::npos) {
            if (matches_cidr(remote, pattern)) {
                return true;
            }
        } else if (pattern.find('*') != std::string::npos) {
            if (wildcard_match(comparable, pattern)) {
                return true;
            }
        } else {
            parsed_ip exact;
            if (parse_ip_address(pattern, exact) && same_ip(remote, exact)) {
                return true;
            }
        }
    }
    return false;
}

} // namespace

openloader_access_control::openloader_access_control(std::string path)
    : path(trim(std::move(path))) {
}

bool openloader_access_control::configured() const {
    return !path.empty();
}

openloader_access_decision openloader_access_control::authorize(
    const std::string & remote_address,
    const std::string & api_key,
    bool validate_api_key) {
    if (!configured()) {
        return openloader_access_decision::allowed;
    }

    std::lock_guard<std::mutex> lock(mutex);
    refresh_locked();
    if (!current.valid) {
        return openloader_access_decision::policy_unavailable;
    }
    if (current.ip_allowlist_enabled && !ip_allowed(remote_address, current.ip_allowlist)) {
        return openloader_access_decision::ip_not_allowed;
    }
    if (validate_api_key && current.api_key_enabled && !key_allowed(api_key, current.api_key_hashes)) {
        return openloader_access_decision::invalid_api_key;
    }
    return openloader_access_decision::allowed;
}

std::string openloader_access_control::last_error() const {
    std::lock_guard<std::mutex> lock(mutex);
    return error;
}

void openloader_access_control::refresh_locked() {
    const auto now = std::chrono::steady_clock::now();
    if (now < next_refresh) {
        return;
    }
    next_refresh = now + refresh_interval;

    snapshot next;
    try {
        std::ifstream input(path, std::ios::binary);
        if (!input) {
            throw std::runtime_error("cannot open access-control snapshot");
        }
        const std::string content((std::istreambuf_iterator<char>(input)), std::istreambuf_iterator<char>());
        const common_json root = common_json::parse(content);
        if (!root.is_object() || root.value("version", 0) != 1) {
            throw std::runtime_error("unsupported access-control snapshot version");
        }
        const common_json & policy = root.at("policy");
        if (!policy.is_object()) {
            throw std::runtime_error("access-control policy is invalid");
        }
        next.api_key_enabled = policy.at("api_key_enabled").get<bool>();
        next.ip_allowlist_enabled = policy.at("ip_allowlist_enabled").get<bool>();
        const common_json & ip_allowlist = policy.at("ip_allowlist");
        if (!ip_allowlist.is_null()) {
            next.ip_allowlist = ip_allowlist.get<std::vector<std::string>>();
        }
        if (next.ip_allowlist.size() > 256
            || !std::all_of(next.ip_allowlist.begin(), next.ip_allowlist.end(), validate_pattern)) {
            throw std::runtime_error("access-control IP allowlist is invalid");
        }

        const common_json & keys = root.at("keys");
        if ((!keys.is_null() && !keys.is_array()) || keys.size() > 100) {
            throw std::runtime_error("access-control keys are invalid");
        }
        for (size_t index = 0; index < keys.size(); ++index) {
            std::string hash = lowercase(keys.at(index).at("hash").get<std::string>());
            if (!is_valid_hash(hash)) {
                throw std::runtime_error("access-control key hash is invalid");
            }
            next.api_key_hashes.push_back(std::move(hash));
        }
        if (next.api_key_enabled && next.api_key_hashes.empty()) {
            throw std::runtime_error("API key validation is enabled without any keys");
        }
        if (next.ip_allowlist_enabled && next.ip_allowlist.empty()) {
            throw std::runtime_error("IP allowlist is enabled without any entries");
        }
        next.valid = true;
        current = std::move(next);
        error.clear();
    } catch (const std::exception & exception) {
        current = snapshot {};
        error = exception.what();
    }
}
