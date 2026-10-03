#include <cerrno>
#include <condition_variable>
#include <cstdlib>
#include <cstring>
#include <fcntl.h>
#include <future>
#include <iostream>
#include <list>
#include <mutex>
#include <poll.h>
#include <stdexcept>
#include <string>
#include <sys/socket.h>
#include <sys/resource.h>
#include <chrono>
#include <thread>
#include <unistd.h>
#include <vector>
#include "ring-socket-thread.h"
int main(int argc, char** argv) {
  int fds[2];
  if (socketpair(AF_UNIX, SOCK_STREAM, 0, fds)) return 1;
  if (argc == 2 && std::string(argv[1]) == "--waiting") {
    rusage before{}, after{};
    getrusage(RUSAGE_SELF, &before);
    {
      SocketThread endpoint(fds[0]);
      char received = 0, sent = 'S';
      auto pending = endpoint.recv(&received, 1);
      std::this_thread::sleep_for(std::chrono::milliseconds(500));
      // 接收等待中仍須可加入傳送，不能被 queue mutex 或 poll 卡住。
      auto outgoing = endpoint.send(&sent, 1);
      if (outgoing.wait_for(std::chrono::milliseconds(100)) != std::future_status::ready) return 1;
      char peer = 0;
      if (::recv(fds[1], &peer, 1, 0) != 1 || peer != sent) return 1;
      std::this_thread::sleep_for(std::chrono::milliseconds(500));
      if (::send(fds[1], &sent, 1, 0) != 1) return 1;
      pending.get();
      if (received != sent) return 1;
    }
    getrusage(RUSAGE_SELF, &after);
    auto seconds = [](timeval t) { return t.tv_sec + t.tv_usec / 1000000.0; };
    double used = seconds(after.ru_utime) + seconds(after.ru_stime)
        - seconds(before.ru_utime) - seconds(before.ru_stime);
    std::cout << "等待 1 秒的程序 CPU 時間：" << used << " 秒\n";
    close(fds[0]); close(fds[1]);
    return 0;
  }
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
