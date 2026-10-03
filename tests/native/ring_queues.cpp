#include <cerrno>
#include <condition_variable>
#include <cstdlib>
#include <cstring>
#include <fcntl.h>
#include <future>
#include <iostream>
#include <list>
#include <mutex>
#include <stdexcept>
#include <string>
#include <sys/socket.h>
#include <thread>
#include <unistd.h>
#include <vector>
#include "ring-socket-thread.h"
int main(int argc, char** argv) {
  int fds[2];
  if (socketpair(AF_UNIX, SOCK_STREAM, 0, fds)) return 1;
  if (argc == 2) {
    SocketThread endpoint(fds[0]);
    close(fds[1]);
    char byte = 0;
    auto pending = std::string(argv[1]) == "--peer-recv"
        ? endpoint.recv(&byte, 1) : endpoint.send(&byte, 1);
    pending.get();
    return 1; // 斷線必須明確以非零狀態退出，不能回傳成功或持續等待。
  }
  {
    SocketThread left(fds[0]), right(fds[1]);
    std::vector<std::thread> callers;
    for (int side = 0; side < 2; ++side) for (int c = 0; c < 4; ++c) {
      callers.emplace_back([&, side] {
        auto& from = side ? right : left;
        auto& to = side ? left : right;
        for (int i = 0; i < 64; ++i) {
          std::vector<char> input(4096, side ? 'B' : 'A'), output(4096);
          auto sent = from.send(input.data(), input.size());
          auto received = to.recv(output.data(), output.size());
          sent.get(); received.get();
          if (input != output) std::abort();
        }
      });
    }
    for (auto& caller : callers) caller.join();
  }
  close(fds[0]); close(fds[1]);
  std::cout << "Socket 佇列並行傳輸完成\n";
}
