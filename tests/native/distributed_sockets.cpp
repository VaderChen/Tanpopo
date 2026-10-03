#include <cerrno>
#include <fcntl.h>
#include <iostream>
#include <netinet/in.h>
#include <stdexcept>
#include <string>
#include <sys/socket.h>
#include <unistd.h>

#include "mlx/distributed/utils.h"

using namespace mlx::core::distributed::detail;

static int descriptorCount() {
  int count = 0;
  for (int fd = 0; fd < 1024; ++fd) {
    if (fcntl(fd, F_GETFD) != -1) ++count;
  }
  return count;
}

int main() {
  // 先取得未使用的本機埠再關閉，對未監聽端點驗證重試與錯誤回報。
  int reserved = socket(AF_INET, SOCK_STREAM, 0);
  sockaddr_in endpoint{};
  endpoint.sin_family = AF_INET;
  endpoint.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
  if (reserved < 0 || bind(reserved, reinterpret_cast<sockaddr*>(&endpoint), sizeof(endpoint))) return 1;
  socklen_t length = sizeof(endpoint);
  if (getsockname(reserved, reinterpret_cast<sockaddr*>(&endpoint), &length)) return 1;
  auto address = parse_address("127.0.0.1", std::to_string(ntohs(endpoint.sin_port)));
  close(reserved);
  const int before = descriptorCount();
  for (int run = 0; run < 4; ++run) {
    int attempts = 0;
    try {
      auto connection = TCPSocket::connect("[smoke]", address, 3, 1, [&](int attempt, int) {
        const int observed = errno;
        if (observed != ECONNREFUSED) throw std::logic_error("回呼 attempt=" + std::to_string(attempt) + " errno=" + std::to_string(observed));
        ++attempts;
        errno = EINVAL; // 模擬日誌與等待覆蓋 errno。
      });
      throw std::logic_error("未監聽的埠不應連線成功");
    } catch (const std::runtime_error& error) {
      if (std::string(error.what()).find("error: " + std::to_string(ECONNREFUSED) + ",") == std::string::npos || attempts != 3) {
        std::cerr << error.what() << '\n';
        return 1;
      }
    }
    if (descriptorCount() != before) throw std::logic_error("重試洩漏 socket");
  }
  try {
    auto connection = TCPSocket::connect("[smoke]", address, 3, 1, [](int, int) {
      throw std::runtime_error("callback failure");
    });
  } catch (const std::runtime_error&) {}
  if (descriptorCount() != before) throw std::logic_error("回呼例外洩漏 socket");
  reserved = socket(AF_INET, SOCK_STREAM, 0);
  if (reserved < 0 || bind(reserved, reinterpret_cast<sockaddr*>(&endpoint), sizeof(endpoint)) || listen(reserved, 1)) return 1;
  {
    auto connection = TCPSocket::connect("[smoke]", address, 1, 0);
    const int incoming = accept(reserved, nullptr, nullptr);
    if (incoming < 0) return 1;
    close(incoming);
  }
  close(reserved);
  if (descriptorCount() != before) throw std::logic_error("成功連線後未釋放 socket");
  std::cout << "TCP 連線原始錯誤碼、重試、回呼例外與 socket 回收 Smoke 通過\n";
}
